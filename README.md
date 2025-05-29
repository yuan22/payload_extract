# Payload Extract
Golang android payload extraction, another python impl here:[payload_extract_py](https://github.com/affggh/payload_extract_py)
## Function
- Support print payload informatoin
- Support extract from zip or url rom file
- Multi thread support
- Native c lzma decompress performance
# Build
## Native
- Install gcc    
example on archlinux:
```sh
sudo pacman -S gcc base-devel
```
- Build
```sh
go build -ldflags="-s -w" -trimpath -o payload_extract_go cmd/main.go
```

## Example build for windows on archlinux

- Install mingw32    
```sh
sudo pacman -S mingw-w64-gcc
```
- Install vcpkg and setup liblzma
```sh
vcpkg install --triplet=x64-mingw-static liblzma
```
- Build with cross toolchain
_Example build for windows:_    
```sh
export GOOS=windows
export GOARCH=amd64
export CC=x86_64-w64-mingw32-gcc
export CXX=x86_64-w64-mingw32-g++
export CGO_ENABLED=1

cmake -B build -G Ninja \
  -DCMAKE_BUILD_TYPE=Release \
  -DSTATIC=ON \
  -DVCPKG_HOME=/path/to/vcpkg \
  -DVCPKG_TARGET_TRIPLET=x64-mingw-static \
  -DCMAKE_TOOLCHAIN_FILE=/path/to/vcpkg/scripts/buildsystems/vcpkg.cmake
cmake --build build

# output:build/payload_extract_go.exe
```

# Usage
```sh
Usage of ./main:
  -P    do not extract, print partitions info
  -T int
        thread pool workers (default 12)
  -X value
        extract partitions
  -i string
        input payload bin/zip/url
  -o string
        output directory (default "out")
```

# proto copied from
- [payload_dumper_go](https://github.com/ssut/payload-dumper-go/blob/main/update_metadata.proto)

# Thanks
- [skkk](https://github.com/sekaiacg)
