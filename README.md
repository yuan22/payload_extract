# Payload Extract
Golang impl this, another python impl here:[payload_extract_py](https://github.com/affggh/payload_extract_py)

# Build
## Native
- Install gcc    
example on archlinux:`sudo pacman -S gcc base-devel`
- Build
`go build -ldflags="-s -w" -trimpath -o payload_extract_go cmd/main.go`

## Example build for windows on archlinux
- Install mingw32    
```sudo pacman -S mingw-w64-gcc```
- Build with cross toolchain
_Example build for windows:_    
```GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ CGO_ENABLED=1 go build -ldflags="-s -w" -trimpath -o payload_extract_go.exe cmd/main.go```

# Usage
```
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
