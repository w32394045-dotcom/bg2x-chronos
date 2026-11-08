# Project Chronos 鈥?native compute layer (C++17)
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
if(UNIX AND NOT APPLE)
  set_target_properties(chronos PROPERTIES VERSION ${PROJECT_VERSION} SOVERSION 1)
endif()
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
  add_executable(chronos_tests
      tests/test_main.cpp
      tests/test_tiling.cpp
      tests/test_blending.cpp
      tests/test_resample.cpp
      tests/test_image_io.cpp
      tests/test_pipeline.cpp
      tests/test_c_api.cpp
      tests/test_threads.cpp)
  target_include_directories(chronos_tests PRIVATE
      "${CMAKE_CURRENT_SOURCE_DIR}/tests"
      "${CMAKE_CURRENT_SOURCE_DIR}/include"
      "${CMAKE_CURRENT_SOURCE_DIR}/src")
  target_link_libraries(chronos_tests PRIVATE chronos_static)
  if(MINGW)
    target_link_options(chronos_tests PRIVATE -static)
  endif()
  if(CHRONOS_SANITIZE AND NOT MSVC)
    target_compile_options(chronos_tests PRIVATE ${CHRONOS_SANITIZE_FLAGS})
    target_link_options(chronos_tests PRIVATE ${CHRONOS_SANITIZE_FLAGS})
  endif()

  # One ctest entry per suite keeps failures readable.
  foreach(suite tiling blending resample image_io pipeline c_api threads)
    add_test(NAME native_${suite} COMMAND chronos_tests --suite ${suite})
  endforeach()
  add_test(NAME native_all COMMAND chronos_tests)
  add_test(NAME native_cli_info COMMAND chronos_native_cli --info)
  set_tests_properties(native_cli_info PROPERTIES
      PASS_REGULAR_EXPRESSION "stub.*true")

  # End-to-end through the shared library: the c_api suite writes the fixture,
  # then the CLI (linked against chronos.dll) upscales it.
  add_test(NAME native_cli_process COMMAND chronos_native_cli
      --in chronos_cli_fixture.png --out chronos_cli_fixture_out.png
      --scale 2 --tile 16 --overlap 4 --threads 2)
  set_tests_properties(native_cli_process PROPERTIES
      FIXTURES_REQUIRED chronos_cli_fixture
      PASS_REGULAR_EXPRESSION "done")
  set_tests_properties(native_c_api PROPERTIES FIXTURES_SETUP chronos_cli_fixture)
endif()

# --- install ----------------------------------------------------------------
install(TARGETS chronos
    RUNTIME DESTINATION bin
    LIBRARY DESTINATION lib
    ARCHIVE DESTINATION lib)
install(DIRECTORY include/ DESTINATION include)

message(STATUS "chronos: version ${PROJECT_VERSION}, OpenVINO=${CHRONOS_WITH_OPENVINO}, "
               "tests=${CHRONOS_BUILD_TESTS}, sanitize=${CHRONOS_SANITIZE}")
