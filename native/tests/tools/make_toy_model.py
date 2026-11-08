#!/usr/bin/env python3
"""Generate the tiny ONNX model used by the native ONNX Runtime tests.

The model is self-made for this repository (analytic weights, no published model
weights are copied), so the generated file is license clean and only a few KB.
It is committed as native/tests/fixtures/toy_sr_x2.onnx so CI needs no Python.

Architecture (2x super resolution, opset 13):

    Conv(3 -> 8, 3x3, pad 1) -> LeakyRelu(alpha=0.1)
      -> Conv(8 -> 12, 3x3, pad 1) -> PixelShuffle(2)

    input  [1, 3, height, width] float32, dynamic height/width
    output [1, 3, 2*height, 2*width] float32, kept inside [0, 1]

PixelShuffle: ONNX has no operator with that name. The ONNX equivalent of
PyTorch's PixelShuffle(r) is DepthToSpace with mode "DCR" and the same
blocksize, so that is what this graph contains.

Usage:
    python tools/make_toy_model.py [--out PATH] [--verify]
    python tools/make_toy_model.py --input-size 32 [--verify]

The default output is native/tests/fixtures/toy_sr_x2.onnx (dynamic shapes).
--input-size N produces native/tests/fixtures/toy_sr_x2_staticN.onnx instead,
used to test the backend's static-shape / fixed-tile-size code path.

Requires:
    pip install onnx          (numpy is pulled in automatically)
Optional verification (output shape/range, tiling invariance):
    pip install onnxruntime
"""

import argparse
import os

import numpy as np
import onnx
from onnx import TensorProto, helper, numpy_helper


def build_model(input_size=0):
    """Builds the model with deterministic, bounded weights.

    input_size == 0 leaves height/width dynamic (the main fixture). A positive
    value pins a static square input, which exercises the backend's static shape
    and fixed tile size code paths.
    """
    # Conv1 (3 -> 8, 3x3): amplitudes are small so LeakyRelu stays in [-0.1, 1].
    w1 = np.zeros((8, 3, 3, 3), dtype=np.float32)
    for out_channel in range(8):
        for in_channel in range(3):
            for ky in range(3):
                for kx in range(3):
                    w1[out_channel, in_channel, ky, kx] = 0.04 * np.sin(
                        0.7 * (out_channel + 1)
                        + 1.1 * (in_channel + 1)
                        + 1.3 * (ky + 1)
                        + 0.5 * (kx + 1)
                    )
    b1 = np.zeros((8,), dtype=np.float32)

    # Conv2 (8 -> 12, 3x3): sparse center-tap weights, one 0.5 tap per output
    # channel from a distinct input channel, plus bias 0.3. That keeps the
    # output inside [0, 0.9] while preserving real spatial contrast
    # (output = 0.3 + 0.5 * activation), unlike a dense positive kernel which
    # would average the activations into an almost flat image.
    w2 = np.zeros((12, 8, 3, 3), dtype=np.float32)
    for out_channel in range(12):
        w2[out_channel, out_channel % 8, 1, 1] = 0.5
    b2 = np.full((12,), 0.3, dtype=np.float32)

    initializers = [
        numpy_helper.from_array(w1, name="conv1_weight"),
        numpy_helper.from_array(b1, name="conv1_bias"),
        numpy_helper.from_array(w2, name="conv2_weight"),
        numpy_helper.from_array(b2, name="conv2_bias"),
    ]

    nodes = [
        helper.make_node(
            "Conv",
            ["input", "conv1_weight", "conv1_bias"],
            ["conv1"],
            name="conv1",
            pads=[1, 1, 1, 1],
        ),
        helper.make_node("LeakyRelu", ["conv1"], ["act1"], name="act1", alpha=0.1),
        helper.make_node(
            "Conv",
            ["act1", "conv2_weight", "conv2_bias"],
            ["conv2"],
            name="conv2",
            pads=[1, 1, 1, 1],
        ),
        helper.make_node(
            "DepthToSpace",
            ["conv2"],
            ["output"],
            name="pixel_shuffle",
            blocksize=2,
            mode="DCR",
        ),
    ]

    if input_size > 0:
        input_shape = [1, 3, input_size, input_size]
        output_shape = [1, 3, 2 * input_size, 2 * input_size]
    else:
        input_shape = [1, 3, "height", "width"]
        output_shape = [1, 3, "2*height", "2*width"]

    graph = helper.make_graph(
        nodes,
        "toy_sr_x2",
        [helper.make_tensor_value_info("input", TensorProto.FLOAT, input_shape)],
        [helper.make_tensor_value_info("output", TensorProto.FLOAT, output_shape)],
        initializer=initializers,
    )
    model = helper.make_model(
        graph,
        producer_name="chronos-native-tests",
        opset_imports=[helper.make_opsetid("", 13)],
    )
    # Pin IR version 8 (ONNX 1.15 era): supported by every ONNX Runtime 1.15+.
    model.ir_version = 8
    return model


def verify(path):
    """Optional check with ONNX Runtime; prints shape/range and tiling invariance."""
    try:
        import onnxruntime as ort
    except ImportError:
        print("verify: onnxruntime is not installed, skipping runtime verification")
        return

    session = ort.InferenceSession(path, providers=["CPUExecutionProvider"])
    print("verify: ORT", ort.__version__)
    print("verify: inputs ", [(i.name, i.shape) for i in session.get_inputs()])
    print("verify: outputs", [(o.name, o.shape) for o in session.get_outputs()])

    input_shape = session.get_inputs()[0].shape
    static_size = input_shape[2] if isinstance(input_shape[2], int) else 0
    tile_size = static_size if static_size > 0 else 32
    rng = np.random.RandomState(7)
    tile = rng.rand(1, 3, tile_size, tile_size).astype(np.float32)
    output = session.run(None, {"input": tile})[0]
    assert output.shape == (1, 3, 2 * tile_size, 2 * tile_size), output.shape
    print(
        "verify: %dx%d -> %s, range [%.3f, %.3f], std %.4f"
        % (tile_size, tile_size, output.shape, output.min(), output.max(), output.std())
    )
    assert output.min() >= 0.0 and output.max() <= 1.0, "output left [0, 1]"
    assert float(output.std()) > 0.02, "output has too little contrast"

    if static_size > 0:
        print("verify: static input size, tiling invariance is not applicable")
        return

    # Tiling invariance: the model is fully convolutional, so an interior crop
    # must produce exactly the same values as the corresponding region of the
    # full image. Two Conv layers with pad 1 need a 2 input pixel margin at the
    # crop border (4 output pixels after PixelShuffle), so compare the interior
    # only. The C++ test asserts this end to end through the pipeline.
    image = rng.rand(1, 3, 48, 48).astype(np.float32)
    full = session.run(None, {"input": image})[0]
    tile = image[:, :, 8:40, 8:40]
    tile_output = session.run(None, {"input": tile})[0]
    assert tile_output.shape == (1, 3, 64, 64), tile_output.shape
    margin = 4
    reference = full[:, :, 16 + margin : 80 - margin, 16 + margin : 80 - margin]
    interior = tile_output[:, :, margin : 64 - margin, margin : 64 - margin]
    max_float_diff = float(np.abs(interior - reference).max())
    quantized_diff = np.abs(
        np.clip(interior * 255.0 + 0.5, 0, 255).astype(np.int32)
        - np.clip(reference * 255.0 + 0.5, 0, 255).astype(np.int32)
    )
    print(
        "verify: interior tile vs full image max abs diff = %.3e (u8 %d)"
        % (max_float_diff, int(quantized_diff.max()))
    )
    assert max_float_diff < 1e-4, max_float_diff


def main():
    here = os.path.dirname(os.path.abspath(__file__))
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--out",
        default="",
        help="output path (default: native/tests/fixtures/toy_sr_x2[_staticN].onnx)",
    )
    parser.add_argument(
        "--input-size",
        type=int,
        default=0,
        help="pin a static square input size instead of dynamic height/width (0 = dynamic)",
    )
    parser.add_argument(
        "--verify", action="store_true", help="run an optional ONNX Runtime sanity check"
    )
    args = parser.parse_args()

    if not args.out:
        name = (
            "toy_sr_x2.onnx"
            if args.input_size <= 0
            else "toy_sr_x2_static%d.onnx" % args.input_size
        )
        args.out = os.path.join(here, "..", "fixtures", name)

    model = build_model(args.input_size)
    onnx.checker.check_model(model)
    out_path = os.path.abspath(args.out)
    os.makedirs(os.path.dirname(out_path), exist_ok=True)
    onnx.save(model, out_path)
    print("wrote %s (%d bytes)" % (out_path, os.path.getsize(out_path)))
    if args.verify:
        verify(out_path)


if __name__ == "__main__":
    main()
