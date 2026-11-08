# Project Chronos - native compute layer (C++17)
#
# Builds:
#   chronos             shared library: chronos.dll (Windows) / libchronos.so (Linux)
#   chronos_native_cli  phase-1 CLI for manual verification
#   chronos_tests       dependency-free unit/integration tests (ctest)
#
# Contract: docs/CONTRACTS.md sections 1, 2 and 4.

cmake_minimum_required(VERSION 3.16)
project(chronos_native VERSION 1.0.0 LANGUAGES CXX)

option(CHRONOS_WITH_OPENVINO "Build the OpenVINO inference backend" OFF)
option(CHRONOS_BUILD_TESTS "Build the native test suite" ON)
option(CHRONOS_SANITIZE "Enable AddressSanitizer + UBSan (GCC/Clang only)" OFF)
option(CHRONOS_WARNINGS_AS_ERRORS "Treat compiler warnings as errors" OFF)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(NOT CMAKE_BUILD_TYPE AND NOT CMAKE_CONFIGURATION_TYPES)
  set(CMAKE_BUILD_TYPE Release CACHE STRING "Build type" FORCE)
endif()

if(CMAKE_INSTALL_PREFIX_INITIALIZED_TO_DEFAULT)
  set(CMAKE_INSTALL_PREFIX "${CMAKE_CURRENT_SOURCE_DIR}/install" CACHE PATH
      "Install prefix (defaults to native/install)" FORCE)
endif()

# Keep every artifact in the build root: CONTRACTS 搂4 links with
# `-L native/build -lchronos` and copies chronos.dll next to the Go executable.
set(CMAKE_RUNTIME_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}")
set(CMAKE_LIBRARY_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}")
set(CMAKE_ARCHIVE_OUTPUT_DIRECTORY "${CMAKE_BINARY_DIR}")
foreach(config "" _DEBUG _RELEASE _RELWITHDEBINFO _MINSIZEREL)
  set(CMAKE_RUNTIME_OUTPUT_DIRECTORY${config} "${CMAKE_BINARY_DIR}")
  set(CMAKE_LIBRARY_OUTPUT_DIRECTORY${config} "${CMAKE_BINARY_DIR}")
  set(CMAKE_ARCHIVE_OUTPUT_DIRECTORY${config} "${CMAKE_BINARY_DIR}")
endforeach()

set(CHRONOS_SOURCES
    src/stb_impl.cpp
    src/image.cpp
    src/tiling.cpp
    src/blending.cpp
    src/resample.cpp
    src/options.cpp
    src/thread_pool.cpp
    src/backend_stub.cpp
    src/backend_openvino.cpp
    src/platform_io.cpp
    src/ort_loader.cpp
    src/ort_sidecar.cpp
    src/ort_inspect.cpp
    src/backend_ort.cpp
    src/pipeline.cpp
    src/c_api.cpp)

# Everything except the CLI lives in an OBJECT library so the shared library and
# the tests can share one compilation while tests still link the real DLL.
add_library(chronos_objects OBJECT ${CHRONOS_SOURCES})
target_include_directories(chronos_objects
    PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}/include"
    PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}/src" "${CMAKE_CURRENT_SOURCE_DIR}/third_party")
target_compile_definitions(chronos_objects PRIVATE CHRONOS_NATIVE_VERSION="${PROJECT_VERSION}")
set_target_properties(chronos_objects PROPERTIES POSITION_INDEPENDENT_CODE ON)

if(CHRONOS_WITH_OPENVINO)
  find_package(OpenVINO REQUIRED COMPONENTS Runtime)
  target_compile_definitions(chronos_objects PRIVATE CHRONOS_WITH_OPENVINO=1)
  target_link_libraries(chronos_objects PRIVATE openvino::runtime)
endif()

if(MSVC)
  target_compile_options(chronos_objects PRIVATE /W4 /permissive-)
  if(CHRONOS_WARNINGS_AS_ERRORS)
    target_compile_options(chronos_objects PRIVATE /WX)
  endif()
else()
  target_compile_options(chronos_objects PRIVATE -Wall -Wextra)
  if(CHRONOS_WARNINGS_AS_ERRORS)
    target_compile_options(chronos_objects PRIVATE -Werror)
  endif()
  # stb is third-party: compile it without our warning set. Its JPEG writer has a
  # known signed left-shift UB, so UBSan's shift check is disabled for that TU
  # only (the flag is a no-op in non-sanitizer builds).
  set_source_files_properties(src/stb_impl.cpp PROPERTIES
      COMPILE_OPTIONS "-w;-fno-sanitize=shift")
endif()

if(CHRONOS_SANITIZE)
  if(MSVC OR MINGW)
    # MinGW-w64 ships no libasan and MSVC uses a different sanitizer mechanism.
    message(WARNING "CHRONOS_SANITIZE is only supported for Linux/macOS GCC/Clang builds; "
                    "sanitizer flags are disabled for this toolchain")
  else()
    set(CHRONOS_SANITIZE_FLAGS -fsanitize=address,undefined -fno-omit-frame-pointer)
    target_compile_options(chronos_objects PRIVATE ${CHRONOS_SANITIZE_FLAGS})
  endif()
endif()

# --- shared library ---------------------------------------------------------
add_library(chronos SHARED $<TARGET_OBJECTS:chronos_objects>)
target_include_directories(chronos
    PUBLIC $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
           $<INSTALL_INTERFACE:include>)
if(CHRONOS_WITH_OPENVINO)
  target_link_libraries(chronos PRIVATE openvino::runtime)
endif()
set_target_properties(chronos PROPERTIES
    OUTPUT_NAME chronos
    WINDOWS_EXPORT_ALL_SYMBOLS TRUE)
if(WIN32)
  # Contract: the DLL shipped next to the Go executable is `chronos.dll`
  # (MinGW would otherwise name it libchronos.dll).
  set_target_properties(chronos PROPERTIES PREFIX "")
endif()
# NOTE: no VERSION/SOVERSION on purpose. The only consumer is the Go binary and
# the packages ship exactly one file (libs/libchronos.so); a versioned soname
# (libchronos.so.1) would break runtime loading from the packaging tree.
if(MINGW)
  # Self-contained DLL: chronos.dll is shipped next to the Go executable, so it
  # must not depend on libstdc++/libgcc/libwinpthread DLLs (CONTRACTS 搂4).
  target_link_options(chronos PRIVATE -static)
endif()
if(CHRONOS_SANITIZE AND NOT MSVC)
  target_link_options(chronos PRIVATE ${CHRONOS_SANITIZE_FLAGS})
endif()

# --- static convenience library (tests only) --------------------------------
# Tests link the same objects statically so C++ exceptions/RTTI never have to
# cross a DLL boundary (the shared library is exercised by the CLI smoke test).
add_library(chronos_static STATIC $<TARGET_OBJECTS:chronos_objects>)
target_include_directories(chronos_static PUBLIC
    "${CMAKE_CURRENT_SOURCE_DIR}/include")
if(CHRONOS_WITH_OPENVINO)
  target_link_libraries(chronos_static PUBLIC openvino::runtime)
endif()

# --- CLI --------------------------------------------------------------------
add_executable(chronos_native_cli src/cli_main.cpp)
target_link_libraries(chronos_native_cli PRIVATE chronos)
if(CHRONOS_SANITIZE AND NOT MSVC)
  target_compile_options(chronos_native_cli PRIVATE ${CHRONOS_SANITIZE_FLAGS})
  target_link_options(chronos_native_cli PRIVATE ${CHRONOS_SANITIZE_FLAGS})
endif()

# --- tests ------------------------------------------------------------------
if(CHRONOS_BUILD_TESTS)
  enable_testing()

  # Scratch directory for every test artifact.
  #
  # It intentionally lives inside the build tree instead of the system temp dir:
  # the build tree is the only place every runner (CI container, developer shell,
  # confined sandboxes) is guaranteed to be allowed to write. CTest forwards the
  # path as CHRONOS_TEST_TMPDIR so a manually run test binary can never drop files
  # into the repository working tree (see tests/test_util.h).
  set(CHRONOS_TEST_TMPDIR "${CMAKE_CURRENT_BINARY_DIR}/chronos_native_tests")
  file(TO_CMAKE_PATH "${CHRONOS_TEST_TMPDIR}" CHRONOS_TEST_TMPDIR)
  set(CHRONOS_TEST_ENV "CHRONOS_TEST_TMPDIR=${CHRONOS_TEST_TMPDIR}")

  add_executable(chronos_tests
      tests/test_main.cpp
      tests/test_tiling.cpp
      tests/test_blending.cpp
      tests/test_resample.cpp
      tests/test_image_io.cpp
      tests/test_pipeline.cpp
      tests/test_c_api.cpp
      tests/test_ort.cpp
      tests/test_threads.cpp)
  target_include_directories(chronos_tests PRIVATE
      "${CMAKE_CURRENT_SOURCE_DIR}/tests"
      "${CMAKE_CURRENT_SOURCE_DIR}/include"
      "${CMAKE_CURRENT_SOURCE_DIR}/src"
      "${CMAKE_CURRENT_SOURCE_DIR}/third_party")
  target_link_libraries(chronos_tests PRIVATE chronos_static)
  if(MINGW)
    target_link_options(chronos_tests PRIVATE -static)
  endif()
  if(CHRONOS_SANITIZE AND NOT MSVC)
    target_compile_options(chronos_tests PRIVATE ${CHRONOS_SANITIZE_FLAGS})
    target_link_options(chronos_tests PRIVATE ${CHRONOS_SANITIZE_FLAGS})
  endif()

  # One ctest entry per suite keeps failures readable.
  foreach(suite tiling blending resample image_io pipeline c_api threads ort)
    add_test(NAME native_${suite} COMMAND chronos_tests --suite ${suite})
  endforeach()
  add_test(NAME native_all COMMAND chronos_tests)
  add_test(NAME native_cli_info COMMAND chronos_native_cli --info)
  set_tests_properties(native_cli_info PROPERTIES
      PASS_REGULAR_EXPRESSION "stub.*true")

  # The ORT backend needs the dynamically loaded ONNX Runtime library at run
  # time. Without it test_ort.cpp prints a SKIP message and exits with 77, which
  # ctest reports as "Skipped" instead of a failure. The fixture directory is
  # passed through the environment because the repository path may contain
  # non-ASCII characters (a compile definition would be narrowed through the
  # toolchain's ANSI code page on Windows).
  set_tests_properties(native_ort PROPERTIES SKIP_RETURN_CODE 77)
  set(CHRONOS_ORT_TEST_ENV "${CHRONOS_TEST_ENV}"
      "CHRONOS_TEST_FIXTURES_DIR=${CMAKE_CURRENT_SOURCE_DIR}/tests/fixtures")
  if(DEFINED ENV{CHRONOS_ORT_DIR} AND NOT "$ENV{CHRONOS_ORT_DIR}" STREQUAL "")
    list(APPEND CHRONOS_ORT_TEST_ENV "CHRONOS_ORT_DIR=$ENV{CHRONOS_ORT_DIR}")
  endif()
  set_tests_properties(native_ort native_all PROPERTIES ENVIRONMENT "${CHRONOS_ORT_TEST_ENV}")

  # End-to-end through the shared library: the c_api suite writes the fixture,
  # then the CLI (linked against chronos.dll) upscales it. Both sides resolve
  # the same CHRONOS_TEST_TMPDIR, so the file stays out of the working tree.
  add_test(NAME native_cli_process COMMAND chronos_native_cli
      --in "${CHRONOS_TEST_TMPDIR}/chronos_cli_fixture.png"
      --out "${CHRONOS_TEST_TMPDIR}/chronos_cli_fixture_out.png"
      --scale 2 --tile 16 --overlap 4 --threads 2)
  set_tests_properties(native_cli_process PROPERTIES
      FIXTURES_REQUIRED chronos_cli_fixture
      PASS_REGULAR_EXPRESSION "done")
  set_tests_properties(native_c_api PROPERTIES FIXTURES_SETUP chronos_cli_fixture)

  # Every test gets the scratch directory, including the ones that only use it
  # indirectly through the shared chronos_test helpers.
  foreach(test_name native_tiling native_blending native_resample native_image_io
                    native_pipeline native_c_api native_threads native_ort
                    native_all native_cli_info native_cli_process)
    set_tests_properties(${test_name} PROPERTIES ENVIRONMENT "${CHRONOS_TEST_ENV}")
  endforeach()
  # native_ort / native_all need the extra ORT + fixture environment on top.
  set_tests_properties(native_ort native_all PROPERTIES ENVIRONMENT "${CHRONOS_ORT_TEST_ENV}")
endif()

# --- install ----------------------------------------------------------------
install(TARGETS chronos
    RUNTIME DESTINATION bin
    LIBRARY DESTINATION lib
    ARCHIVE DESTINATION lib)
install(DIRECTORY include/ DESTINATION include)

message(STATUS "chronos: version ${PROJECT_VERSION}, OpenVINO=${CHRONOS_WITH_OPENVINO}, "
               "tests=${CHRONOS_BUILD_TESTS}, sanitize=${CHRONOS_SANITIZE}")
message(STATUS "chronos: ONNX Runtime backend is always built and loads "
               "onnxruntime.dll/libonnxruntime.so at run time (CHRONOS_ORT_DIR)")
