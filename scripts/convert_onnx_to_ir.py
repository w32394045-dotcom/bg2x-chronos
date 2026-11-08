#!/usr/bin/env python3
"""Project Chronos - convert a model to OpenVINO IR (FP16).

The optional OpenVINO backend loads OpenVINO IR (``.xml`` + ``.bin``). ONNX
files can also be loaded directly by OpenVINO at runtime, but converting first
gives a faster cold start and allows FP16 compression (roughly half the size).

Usage:
    python scripts/convert_onnx_to_ir.py --input models/real-esrgan-x4plus.onnx
    python scripts/convert_onnx_to_ir.py \
        --input models/real-esrgan-x4plus.onnx \
        --output-dir models/ir \
        --scale 4

Requires:
    pip install openvino          # OpenVINO 2023.3+ ships convert_model/save_model
    # older releases: pip install openvino-dev

The script is deliberately defensive about the OpenVINO Python API because the
import paths changed repeatedly between releases (openvino.convert_model,
openvino.runtime, openvino.tools.ovc / openvino.tools.mo).
"""

import argparse
import os
import sys
import time


def load_openvino():
    """Return (module, description) for whichever OpenVINO Python API exists."""
    try:
        import openvino as ov  # noqa: PLC0415 - optional heavy dependency

        return ov, "openvino"
    except ImportError:
        pass
    try:
        import openvino.runtime as ov  # type: ignore[no-redef]

        return ov, "openvino.runtime"
    except ImportError:
        return None, None


def serialize_legacy(model, xml_path):
    """Serialize a model on releases whose API lives in openvino.runtime."""
    from openvino.runtime import serialize  # noqa: PLC0415

    serialize(model, str(xml_path), compress_to_fp16=True)


def convert_to_ir(ov, source, target_xml):
    """Convert an .onnx file to OpenVINO IR with FP16 weights."""
    # 2023.3+: top-level openvino.convert_model / openvino.save_model.
    if hasattr(ov, "convert_model") and hasattr(ov, "save_model"):
        model = ov.convert_model(str(source))
        ov.save_model(model, str(target_xml), compress_to_fp16=True)
        return

    # 2022.x - early 2023.x: openvino.runtime.convert_model + serialize.
    if hasattr(ov, "convert_model"):
        model = ov.convert_model(str(source))
        serialize_legacy(model, target_xml)
        return

    # Legacy tool front-ends.
    for module_name in ("openvino.tools.ovc", "openvino.tools.mo"):
        try:
            module = __import__(module_name, fromlist=["convert_model"])
        except ImportError:
            continue
        convert_model = getattr(module, "convert_model", None)
        if convert_model is None:
            continue
        model = convert_model(str(source))
        serialize_legacy(model, target_xml)
        return

    raise RuntimeError(
        "this OpenVINO installation exposes neither convert_model nor the "
        "ovc/mo tools; install a newer release with 'pip install openvino'"
    )


def human_size(num):
    for unit in ("B", "KB", "MB", "GB"):
        if num < 1024 or unit == "GB":
            if unit == "B":
                return "%d B" % num
            return "%.1f %s" % (num, unit)
        num /= 1024.0
    return "%d B" % num  # pragma: no cover


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Convert an ONNX model to OpenVINO IR (FP16) for Chronos.",
    )
    parser.add_argument("--input", required=True, help="source .onnx model file")
    parser.add_argument(
        "--output-dir",
        default=None,
        help="directory for the .xml/.bin output (default: next to the input)",
    )
    parser.add_argument(
        "--scale",
        type=int,
        default=None,
        help="native upscale factor of the model; appended to the output name (e.g. _x4)",
    )
    args = parser.parse_args(argv)

    source = os.path.abspath(args.input)
    if not os.path.isfile(source):
        sys.stderr.write("[convert] ERROR: input not found: %s\n" % source)
        return 2
    if not source.lower().endswith(".onnx"):
        sys.stderr.write("[convert] ERROR: expected a .onnx input, got: %s\n" % source)
        return 2

    output_dir = os.path.abspath(args.output_dir) if args.output_dir else os.path.dirname(source)
    try:
        os.makedirs(output_dir, exist_ok=True)
    except OSError as exc:
        sys.stderr.write("[convert] ERROR: cannot create %s: %s\n" % (output_dir, exc))
        return 2

    stem = os.path.splitext(os.path.basename(source))[0]
    if args.scale:
        stem = "%s_x%d" % (stem, args.scale)
    target_xml = os.path.join(output_dir, stem + ".xml")
    target_bin = os.path.join(output_dir, stem + ".bin")

    ov, api_name = load_openvino()
    if ov is None:
        sys.stderr.write(
            "[convert] ERROR: OpenVINO Python package is not installed.\n"
            "  pip install openvino        (or: pip install openvino-dev, legacy)\n"
            "The Chronos desktop app does not need this; it is only for offline\n"
            "ONNX -> IR conversion.\n"
        )
        return 2

    size_before = os.path.getsize(source)
    print("[convert] input : %s (%s)" % (source, human_size(size_before)))
    print("[convert] api   : %s" % api_name)
    print("[convert] output: %s (+ .bin)" % target_xml)

    started = time.time()
    try:
        convert_to_ir(ov, source, target_xml)
    except Exception as exc:  # noqa: BLE001 - report any converter failure cleanly
        sys.stderr.write("[convert] ERROR: conversion failed: %s\n" % exc)
        return 3
    elapsed = time.time() - started

    if not (os.path.isfile(target_xml) and os.path.isfile(target_bin)):
        sys.stderr.write("[convert] ERROR: converter did not produce .xml/.bin\n")
        return 3

    size_xml = os.path.getsize(target_xml)
    size_bin = os.path.getsize(target_bin)
    total_after = size_xml + size_bin
    print("[convert] result: %s (%s), %s (%s), total %s, %.1f%% of source, %.1fs"
          % (target_xml, human_size(size_xml), target_bin, human_size(size_bin),
             human_size(total_after), 100.0 * total_after / size_before, elapsed))
    print("[convert] use it with: chronos upscale -i in.png -o out.png -s %s -m %s --tile 256 --overlap 16"
          % (args.scale or 4, target_xml))
    return 0


if __name__ == "__main__":
    sys.exit(main())
