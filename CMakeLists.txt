cmake_minimum_required(VERSION 3.20)
project(payload_extract_go VERSION 0.1.2)

execute_process(
    COMMAND git rev-parse --short HEAD
    WORKING_DIRECTORY ${CMAKE_SOURCE_DIR}
    OUTPUT_VARIABLE GIT_COMMIT_HASH
    OUTPUT_STRIP_TRAILING_WHITESPACE
)

set(PROJECT_VERSION_FULL "${PROJECT_VERSION}-${GIT_COMMIT_HASH}")

find_package(LibLZMA REQUIRED)

set(TARGET_NAME ${PROJECT_NAME})
if(WIN32)
    set(TARGET_NAME "${TARGET_NAME}.exe")
endif()

set(GO_SOURCES cmd/main.go)
set(GO_FLAGS "-trimpath")

# static build
if (STATIC)
    set(GO_STATIC_FLAGS "-extldflags='-static'")
endif ()

if(CMAKE_BUILD_TYPE STREQUAL "Release")
    list(APPEND GO_FLAGS "-ldflags=-w -s ${GO_STATIC_FLAGS} -linkmode=external -X main.Version=${PROJECT_VERSION_FULL}")
endif()

if (NOT VCPKG_HOME)
    message(ERROR "$VCPKG_HOME must be set!")
endif ()

if (NOT VCPKG_TARGET_TRIPLET)
    message(ERROR "$VCPKG_TRIPLET must be set!")
endif ()

if(NOT LibLZMA_LIBRARY_DIRS)
    set(LibLZMA_LIBRARY_DIRS "${VCPKG_HOME}/installed/${VCPKG_TARGET_TRIPLET}/lib")
endif()

if(NOT LibLZMA_INCLUDE_DIRS)
    set(LibLZMA_INCLUDE_DIRS "${VCPKG_HOME}/installed/${VCPKG_TARGET_TRIPLET}/include")
endif()

set(CGO_ENV
    "CGO_ENABLED=1"
    "CGO_CFLAGS=-O3 -I${LibLZMA_INCLUDE_DIRS}"
    "CGO_LDFLAGS=-O3 -L${LibLZMA_LIBRARY_DIRS} -llzma"
)

if(NOT GO_BIN) 
    set(GO_BIN "go")
endif()

add_custom_target(build_all ALL
    COMMAND ${CMAKE_COMMAND} -E env ${CGO_ENV}
        ${GO_BIN} build ${GO_FLAGS} -o ${CMAKE_BINARY_DIR}/${TARGET_NAME} ${GO_SOURCES}
    WORKING_DIRECTORY ${CMAKE_CURRENT_SOURCE_DIR}
    DEPENDS ${GO_SOURCES}
    COMMENT "Building Go executable with LibLZMA linking"
)

install(FILES ${CMAKE_CURRENT_SOURCE_DIR}/${TARGET_NAME}
    DESTINATION bin
    PERMISSIONS OWNER_READ OWNER_WRITE OWNER_EXECUTE
)