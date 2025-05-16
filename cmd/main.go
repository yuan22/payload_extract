package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// DefaultChunkSize 定义了下载块的默认大小。
// 根据典型文件大小和网络条件进行调整。
const DefaultChunkSize int64 = 1 * 1024 * 1024 // 1 MB (用于测试，实际应用可调大，例如 16MB 或 64MB)

// RangeReadSeeker 实现了 io.ReadSeekCloser 接口，用于支持 HTTP Range 请求的远程文件。
// 它会在内存中缓存下载的块。
type RangeReadSeeker struct {
	url           string
	client        *http.Client
	size          int64 // 远程文件的总大小
	chunkSize     int64 // 下载和缓存的块大小
	currentOffset int64

	cache map[int64][]byte // 缓存: key 是块的起始偏移量, value 是块数据
	mu    sync.RWMutex     // 保护缓存和 currentOffset 的互斥锁

	// 用于初始 HEAD 请求和文件属性
	acceptRanges string
	initialError error // 存储构造函数中的错误
}

// NewRangeReadSeeker 创建一个新的 RangeReadSeeker。
// 它会执行一个初始的 HEAD 请求 (或小的 Range 请求) 来获取文件大小并检查是否支持 Range。
func NewRangeReadSeeker(url string, customChunkSize int64, client *http.Client) (*RangeReadSeeker, error) {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second} // 增加超时
	}
	if customChunkSize <= 0 {
		customChunkSize = DefaultChunkSize
	}

	r := &RangeReadSeeker{
		url:       url,
		client:    client,
		chunkSize: customChunkSize,
		cache:     make(map[int64][]byte),
	}

	// 尝试 HEAD 请求
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		r.initialError = fmt.Errorf("创建 HEAD 请求失败: %w", err)
		return r, r.initialError
	}

	resp, err := client.Do(req)
	if err != nil {
		// HEAD 请求失败，尝试 Range 请求获取第一个字节
		return r.tryInitialRangeRequest()
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent { // 202 和 204 也可能是 HEAD 的有效响应
		// HEAD 状态码不符合预期，仍然尝试 Range 请求
		// fmt.Fprintf(os.Stderr, "HEAD 请求状态码异常: %s, 尝试 Range 请求...\n", resp.Status)
		return r.tryInitialRangeRequest()
	}

	r.acceptRanges = resp.Header.Get("Accept-Ranges")
	if r.acceptRanges != "bytes" {
		// 即使没有明确声明 "bytes"，某些服务器仍然可能支持。
		// 实际的 Range 请求会在不支持时失败。
		// fmt.Fprintf(os.Stderr, "警告: 服务器 %s 未明确声明 'Accept-Ranges: bytes' (收到: '%s')。Range 请求可能失败。\n", url, r.acceptRanges)
	}

	contentLengthStr := resp.Header.Get("Content-Length")
	if contentLengthStr == "" {
		// 如果 HEAD 响应中没有 Content-Length，尝试 Range 请求
		// fmt.Fprintln(os.Stderr, "HEAD 响应中缺少 Content-Length, 尝试 Range 请求...")
		return r.tryInitialRangeRequest()
	}

	parsedSize, err := strconv.ParseInt(contentLengthStr, 10, 64)
	if err != nil {
		r.initialError = fmt.Errorf("解析 Content-Length '%s' 失败: %w", contentLengthStr, err)
		return r, r.initialError
	}
	if parsedSize <= 0 {
		// fmt.Fprintln(os.Stderr, "HEAD 响应中 Content-Length 无效, 尝试 Range 请求...")
		return r.tryInitialRangeRequest()
	}
	r.size = parsedSize
	// fmt.Printf("DEBUG: 通过 HEAD 获取文件大小: %d, Accept-Ranges: %s\n", r.size, r.acceptRanges)
	return r, nil
}

// tryInitialRangeRequest 尝试通过请求第一个字节来获取文件大小。
func (r *RangeReadSeeker) tryInitialRangeRequest() (*RangeReadSeeker, error) {
	// fmt.Fprintln(os.Stderr, "DEBUG: 正在尝试初始 Range 请求 (bytes=0-0)...")
	req, err := http.NewRequest("GET", r.url, nil)
	if err != nil {
		r.initialError = fmt.Errorf("创建初始 Range GET 请求失败: %w", err)
		return r, r.initialError
	}
	req.Header.Set("Range", "bytes=0-0") // 请求第一个字节

	resp, err := r.client.Do(req)
	if err != nil {
		r.initialError = fmt.Errorf("初始 Range GET 请求失败: %w", err)
		return r, r.initialError
	}
	defer resp.Body.Close()

	// 期望 206 Partial Content
	if resp.StatusCode != http.StatusPartialContent {
		// 如果是 200 OK，并且有 Content-Length，说明服务器可能不支持 Range，但返回了整个文件
		if resp.StatusCode == http.StatusOK {
			contentLengthStr := resp.Header.Get("Content-Length")
			if contentLengthStr != "" {
				parsedSize, _ := strconv.ParseInt(contentLengthStr, 10, 64)
				if parsedSize > 0 {
					r.size = parsedSize
					r.acceptRanges = "" // 标记可能不支持 Range
					// fmt.Fprintf(os.Stderr, "警告: 初始 Range 请求返回 200 OK，服务器可能不支持 Range。文件大小: %d\n", r.size)
					// 在这种情况下，我们无法真正实现 Seek，但至少可以读取。
					// 为了简化，这里仍然报错，因为期望的是 Range 支持。
					// 或者，我们可以下载整个文件到缓存（如果文件不大）。
					// 对于一个期望 Range 的 seeker，这通常意味着失败。
					r.initialError = fmt.Errorf("初始 Range 请求返回 %s 而不是 206 Partial Content，服务器可能不支持 Range", resp.Status)
					return r, r.initialError
				}
			}
		}
		r.initialError = fmt.Errorf("初始 Range 请求的响应状态码不是 206 Partial Content: %s", resp.Status)
		return r, r.initialError
	}

	r.acceptRanges = resp.Header.Get("Accept-Ranges") // 再次检查
	if r.acceptRanges != "bytes" {
		// fmt.Fprintf(os.Stderr, "警告: 初始 Range 请求后服务器未明确声明 'Accept-Ranges: bytes' (收到: '%s')。\n", r.acceptRanges)
	}

	contentRange := resp.Header.Get("Content-Range")
	if contentRange == "" {
		r.initialError = fmt.Errorf("初始 Range 请求的响应中缺少 Content-Range 头")
		return r, r.initialError
	}

	// 解析 "bytes Start-End/Total"
	var start, end, total int64
	scanned, _ := fmt.Sscanf(contentRange, "bytes %d-%d/%d", &start, &end, &total)
	if scanned != 3 || total <= 0 {
		// 有些服务器可能会返回 "bytes 0-0/*" 如果文件大小未知，这对于我们的 seeker 是有问题的
		if scanned == 2 { // 尝试解析 "bytes Start-End/*"
			scannedStar, _ := fmt.Sscanf(contentRange, "bytes %d-%d/*", &start, &end)
			if scannedStar == 2 {
				r.initialError = fmt.Errorf("Content-Range ('%s') 表明文件总大小未知，无法支持 Seek", contentRange)
				return r, r.initialError
			}
		}
		r.initialError = fmt.Errorf("无法从 Content-Range '%s' 解析文件总大小", contentRange)
		return r, r.initialError
	}
	r.size = total
	// fmt.Printf("DEBUG: 通过初始 Range 请求获取文件大小: %d, Accept-Ranges: %s, Content-Range: %s\n", r.size, r.acceptRanges, contentRange)
	return r, nil
}

// fetchAndCacheChunk 下载指定的块，将其缓存并返回。
// chunkStartOffset 必须与 r.chunkSize 对齐（或者为0）。
func (r *RangeReadSeeker) fetchAndCacheChunk(chunkStartOffset int64) ([]byte, error) {
	// fmt.Printf("DEBUG: fetchAndCacheChunk called for offset %d\n", chunkStartOffset)
	r.mu.RLock()
	cachedData, found := r.cache[chunkStartOffset]
	r.mu.RUnlock()

	if found {
		// fmt.Printf("DEBUG: Chunk %d found in cache\n", chunkStartOffset)
		return cachedData, nil
	}

	// fmt.Printf("DEBUG: Chunk %d NOT in cache, downloading...\n", chunkStartOffset)
	// 块不在缓存中，需要下载

	// 确保 chunkStartOffset 对齐，除非它是文件的最后一个（可能更小的）块的起始位置
	// 或者 chunkStartOffset 是 0
	if chunkStartOffset%r.chunkSize != 0 && chunkStartOffset != 0 {
		// 理论上，调用 Read 的逻辑应该确保这一点，或者我们在这里处理非对齐的内部请求
		// 为了简单起见，我们假设调用者（Read方法）会处理好块的对齐
	}

	chunkEndOffset := chunkStartOffset + r.chunkSize - 1
	if chunkEndOffset >= r.size {
		chunkEndOffset = r.size - 1
	}

	if chunkStartOffset >= r.size { // 如果请求的起始位置已经超出文件末尾
		return nil, io.EOF
	}

	req, err := http.NewRequest("GET", r.url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 GET 请求失败: %w", err)
	}

	rangeHeader := fmt.Sprintf("bytes=%d-%d", chunkStartOffset, chunkEndOffset)
	req.Header.Set("Range", rangeHeader)
	// fmt.Printf("DEBUG: HTTP GET Range: %s\n", rangeHeader)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET 请求 Range %s 失败: %w", rangeHeader, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		// 如果是 200 OK，并且 Content-Length 与请求的范围不匹配，则服务器未遵循 Range
		if resp.StatusCode == http.StatusOK {
			// 这种情况意味着服务器可能不支持 Range 请求，或者我们的请求有问题
			// 对于 seeker 来说，这是一个严重的问题
			return nil, fmt.Errorf("服务器对 Range 请求 %s 返回 %s，期望 %d (Partial Content)", rangeHeader, resp.Status, http.StatusPartialContent)
		}
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("range 请求 %s 响应状态码错误: %s (body: %s)", rangeHeader, resp.Status, string(bodyBytes))
	}

	// 验证 Content-Range 是否与请求的范围匹配（可选，但有助于调试）
	// contentRange := resp.Header.Get("Content-Range")
	// fmt.Printf("DEBUG: Received Content-Range: %s for requested range %s\n", contentRange, rangeHeader)

	chunkData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 Range %s 的响应体失败: %w", rangeHeader, err)
	}

	actualReadLength := int64(len(chunkData))
	expectedLength := chunkEndOffset - chunkStartOffset + 1
	if actualReadLength != expectedLength {
		// 这可能发生在文件末尾，当请求的块超出文件实际大小时
		// 只要 actualReadLength > 0 并且 chunkStartOffset + actualReadLength <= r.size 即可
		if chunkStartOffset+actualReadLength > r.size && r.size > 0 { // r.size > 0 确保 size 已知
			//  fmt.Fprintf(os.Stderr, "警告: 读取的字节数 (%d) 与期望的字节数 (%d) 不符，但可能已到文件末尾。Range: %s\n", actualReadLength, expectedLength, rangeHeader)
		} else if actualReadLength == 0 && expectedLength > 0 { // 读取到0字节但期望更多，可能是EOF
			if chunkStartOffset >= r.size {
				return nil, io.EOF
			}
		} else if actualReadLength != expectedLength {
			// fmt.Fprintf(os.Stderr, "警告: 读取的字节数 (%d) 与期望的字节数 (%d) 不符。Range: %s. 服务器可能未完全遵循请求。\n", actualReadLength, expectedLength, rangeHeader)
			//  如果读取的字节数少于预期，但并非文件末尾，这可能表明服务器行为异常
			//  但只要有数据，我们就缓存并使用它
		}
	}
	if actualReadLength == 0 && chunkStartOffset < r.size {
		// 请求范围内但没有数据返回，这不应该发生除非是EOF的精确边界
		//  fmt.Fprintf(os.Stderr, "警告: 请求范围 %s 内未返回数据，但当前偏移量 %d 小于文件大小 %d\n", rangeHeader, chunkStartOffset, r.size)
		return nil, io.ErrUnexpectedEOF // 或者一个更具体的错误
	}
	if actualReadLength == 0 && chunkStartOffset >= r.size {
		return nil, io.EOF
	}

	// fmt.Printf("DEBUG: 成功下载 %d字节 用于块起始 %d\n", len(chunkData), chunkStartOffset)

	r.mu.Lock()
	r.cache[chunkStartOffset] = chunkData
	r.mu.Unlock()

	return chunkData, nil
}

// Read 最多读取 len(p) 字节到 p 中。
// 返回读取的字节数 (0 <= n <= len(p)) 以及遇到的任何错误。
func (r *RangeReadSeeker) Read(p []byte) (n int, err error) {
	if r.initialError != nil {
		return 0, fmt.Errorf("reader 初始化错误: %w", r.initialError)
	}
	if r.size == 0 { // 如果在构造函数中未能获取大小
		// 尝试再次获取大小，这可能在 tryInitialRangeRequest 内部设置 initialError
		_, err := r.tryInitialRangeRequest()
		if err != nil {
			return 0, fmt.Errorf("Read 时无法确定文件大小: %w", r.initialError)
		}
		if r.size == 0 && r.initialError == nil { // 仍然无法获取大小
			return 0, fmt.Errorf("Read 时文件大小未知且无法确定")
		}
		if r.initialError != nil { // 如果 tryInitialRangeRequest 设置了错误
			return 0, r.initialError
		}
	}

	r.mu.Lock() // 写锁保护 currentOffset 和 cache 访问的协调
	if r.currentOffset >= r.size {
		r.mu.Unlock()
		return 0, io.EOF
	}

	bytesToRead := len(p)
	if r.currentOffset+int64(bytesToRead) > r.size {
		bytesToRead = int(r.size - r.currentOffset)
	}

	if bytesToRead == 0 { // 可能在文件末尾或请求0字节
		r.mu.Unlock()
		if r.currentOffset >= r.size {
			return 0, io.EOF
		}
		return 0, nil
	}
	// fmt.Printf("DEBUG: Read() called. currentOffset: %d, len(p): %d, bytesToRead: %d, fileSize: %d\n", r.currentOffset, len(p), bytesToRead, r.size)

	bytesCopied := 0
	currentReadOffsetInRequest := r.currentOffset // 本次 Read 操作开始时的偏移量

	// 释放锁，以便 fetchAndCacheChunk 可以获取它自己的锁
	// 但这会使 currentOffset 的更新变得复杂，因为它可能在 fetch 期间被 Seek 更改
	// 更简单的模型是在整个 Read 操作期间保持锁，或者使用更细粒度的锁/通道
	// 为了这里的简单性，暂时在整个关键区域保持锁，但意识到 fetchAndCacheChunk 会阻塞。
	// 一个改进：fetchAndCacheChunk 可以接收一个回调来填充缓存，而不是直接返回数据然后加锁。

	// defer r.mu.Unlock() // 如果在这里 defer，那么 fetchAndCacheChunk 内部的锁获取会死锁

	// 循环直到 p 被填满或到达文件末尾
	for bytesCopied < bytesToRead {
		// 确定当前偏移量所在的块
		chunkStartOffset := (currentReadOffsetInRequest / r.chunkSize) * r.chunkSize
		offsetInChunk := currentReadOffsetInRequest % r.chunkSize

		r.mu.Unlock() // 在 fetchAndCacheChunk 之前解锁
		// fmt.Printf("DEBUG: Read needs chunk starting at %d. Current read offset in request: %d, Offset in this chunk: %d\n", chunkStartOffset, currentReadOffsetInRequest, offsetInChunk)
		chunkData, errFetch := r.fetchAndCacheChunk(chunkStartOffset)
		r.mu.Lock() // 重新获取锁

		if errFetch != nil {
			if errFetch == io.EOF && bytesCopied > 0 { // 如果已经复制了一些数据，则返回它们
				r.currentOffset += int64(bytesCopied)
				// fmt.Printf("DEBUG: Read returning partial data (%d bytes) due to EOF from fetch.\n", bytesCopied)
				return bytesCopied, nil
			}
			// fmt.Printf("DEBUG: Read error from fetchAndCacheChunk: %v\n", errFetch)
			// 不更新 currentOffset，因为没有成功读取
			return bytesCopied, errFetch // 返回已复制的字节数和错误
		}

		if chunkData == nil { // 应该被 errFetch 处理，但作为安全措施
			// fmt.Println("DEBUG: Read received nil chunkData without error, implies EOF or issue.")
			if bytesCopied > 0 {
				r.currentOffset += int64(bytesCopied)
				return bytesCopied, nil
			}
			return 0, io.EOF
		}

		// 从块中复制数据到 p
		bytesToCopyFromChunk := int64(len(chunkData)) - offsetInChunk
		if bytesToCopyFromChunk <= 0 {
			// 这不应该发生，如果发生了，意味着 fetchAndCacheChunk 的逻辑或我们的偏移计算有问题
			// 或者我们请求的 currentReadOffsetInRequest 已经超出了实际获取的 chunkData 的范围
			// 例如，如果 chunkStartOffset 对应的 chunk 比期望的小（文件末尾的短块）
			// fmt.Printf("DEBUG: Read - no bytes to copy from chunk. OffsetInChunk: %d, len(chunkData): %d. currentReadOffsetInRequest: %d, chunkStartOffset: %d\n", offsetInChunk, len(chunkData), currentReadOffsetInRequest, chunkStartOffset)
			if bytesCopied > 0 { // 如果之前已经复制过数据
				r.currentOffset += int64(bytesCopied)
				return bytesCopied, nil
			}
			return 0, io.EOF // 可能意味着我们精确地在块的末尾，并且这个块是文件的末尾
		}

		spaceLeftInP := bytesToRead - bytesCopied
		if bytesToCopyFromChunk > int64(spaceLeftInP) {
			bytesToCopyFromChunk = int64(spaceLeftInP)
		}

		// fmt.Printf("DEBUG: Read - Copying %d bytes from chunk (len %d, offset %d) to p (offset %d)\n", bytesToCopyFromChunk, len(chunkData), offsetInChunk, bytesCopied)
		copy(p[bytesCopied:bytesCopied+int(bytesToCopyFromChunk)], chunkData[offsetInChunk:offsetInChunk+bytesToCopyFromChunk])

		bytesCopied += int(bytesToCopyFromChunk)
		currentReadOffsetInRequest += bytesToCopyFromChunk

		if currentReadOffsetInRequest >= r.size {
			// fmt.Printf("DEBUG: Read reached end of file. currentReadOffsetInRequest: %d, r.size: %d\n", currentReadOffsetInRequest, r.size)
			break // 到达文件末尾
		}
	}
	r.currentOffset = currentReadOffsetInRequest // 更新全局偏移量
	// fmt.Printf("DEBUG: Read finished. Copied %d bytes. New r.currentOffset: %d\n", bytesCopied, r.currentOffset)

	// 如果没有复制任何字节，但没有错误，检查是否是 EOF
	if bytesCopied == 0 && r.currentOffset >= r.size {
		r.mu.Unlock()
		return 0, io.EOF
	}
	r.mu.Unlock()
	return bytesCopied, nil
}

// Seek 设置下一次 Read 或 Write 的偏移量。
// offset 是相对于 whence 的偏移量，whence 可以是 io.SeekStart, io.SeekCurrent, 或 io.SeekEnd。
// 返回新的偏移量和错误（如果有）。
func (r *RangeReadSeeker) Seek(offset int64, whence int) (int64, error) {
	if r.initialError != nil {
		return 0, fmt.Errorf("reader 初始化错误: %w", r.initialError)
	}
	if r.size <= 0 && (whence == io.SeekEnd || r.initialError == nil) { // 如果大小未知且需要 SeekEnd，或者没有初始化错误（意味着应该有大小）
		// 尝试获取大小，这可能会设置 r.initialError
		// fmt.Fprintln(os.Stderr, "DEBUG: Seek - file size unknown, attempting to determine...")
		_, err := r.tryInitialRangeRequest() // 这会尝试填充 r.size
		if err != nil {
			return r.currentOffset, fmt.Errorf("Seek 时无法确定文件大小: %w", r.initialError)
		}
		if r.size <= 0 && r.initialError == nil {
			return r.currentOffset, fmt.Errorf("Seek 时文件大小未知且无法确定")
		}
		if r.initialError != nil {
			return r.currentOffset, r.initialError
		}
		// fmt.Fprintf(os.Stderr, "DEBUG: Seek - file size determined: %d\n", r.size)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = r.currentOffset + offset
	case io.SeekEnd:
		if r.size <= 0 {
			return r.currentOffset, fmt.Errorf("无法从文件末尾 Seek：文件大小未知")
		}
		newOffset = r.size + offset // offset 通常为负或零
	default:
		return r.currentOffset, fmt.Errorf("无效的 whence: %d", whence)
	}

	if newOffset < 0 {
		return r.currentOffset, fmt.Errorf("Seek 导致负偏移量: %d", newOffset)
	}

	// Seek 可以超出文件末尾，后续 Read 会返回 EOF
	// if newOffset > r.size {
	// newOffset = r.size // 或者允许超出，让 Read 处理 EOF
	// }

	r.currentOffset = newOffset
	// fmt.Printf("DEBUG: Seek successful. New offset: %d (original offset: %d, whence: %d, fileSize: %d)\n", r.currentOffset, offset, whence, r.size)
	return r.currentOffset, nil
}

// Close 关闭 RangeReadSeeker。目前它只是一个占位符，因为内存缓存会被垃圾回收。
// 如果使用临时文件等资源，则需要在这里清理。
func (r *RangeReadSeeker) Close() error {
	if r.initialError != nil {
		// 即使初始化失败，也允许调用 Close
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cache = nil // 帮助GC，尽管不是严格必需
	// fmt.Println("DEBUG: RangeReadSeeker Closed.")
	return nil
}

func main() {
	// 示例：一个公开的文本文件或图片 URL
	// 请替换为实际的支持 Range 请求的文件链接
	// 一个小的文本文件，方便测试
	// fileURL := "https://www.ietf.org/rfc/rfc2616.txt" // 较大的文件
	fileURL := "https://www.google.com/robots.txt" // 较小的文件

	// 你也可以用一个本地文件服务器来测试，例如 Go 的 http.FileServer
	// go run main.go & (启动一个简单的本地服务器)
	// fileURL := "http://localhost:8080/your-test-file.dat"

	fmt.Printf("正在尝试从 URL 下载: %s\n", fileURL)

	// 使用自定义的 ChunkSize
	customChunk := int64(100) // 100 字节的块，用于更细致地测试缓存和分块逻辑
	// customChunk := DefaultChunkSize

	rs, err := NewRangeReadSeeker(fileURL, customChunk, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建 RangeReadSeeker 失败: %v\n", err)
		return
	}
	defer rs.Close()

	fmt.Printf("RangeReadSeeker 创建成功。文件大小: %d bytes, ChunkSize: %d bytes\n", rs.size, rs.chunkSize)

	// 1. 读取开头一部分
	buffer := make([]byte, 150) // 读取比 chunk 大一点，测试跨块
	fmt.Println("\n--- 1. 读取开头 150 字节 ---")
	n, err := rs.Read(buffer)
	if err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "首次读取错误: %v\n", err)
		return
	}
	fmt.Printf("首次读取 %d 字节: \"%s...\"\n", n, string(buffer[:n]))
	if n < 150 && n < int(rs.size) {
		fmt.Printf("注意: 读取的字节数 (%d) 少于请求的字节数 (150)，可能已达文件末尾或发生错误。\n", n)
	}

	// 2. Seek 到某个位置并读取
	seekOffset := int64(50)
	if rs.size > 0 && seekOffset >= rs.size {
		seekOffset = rs.size / 2 // 如果文件太小，调整 seekOffset
	}
	fmt.Printf("\n--- 2. Seek 到偏移量 %d 并读取 70 字节 ---\n", seekOffset)
	newPos, err := rs.Seek(seekOffset, io.SeekStart)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Seek 错误: %v\n", err)
		return
	}
	if newPos != seekOffset {
		fmt.Fprintf(os.Stderr, "Seek 后的位置 (%d) 与期望位置 (%d) 不符\n", newPos, seekOffset)
	}
	fmt.Printf("Seek 成功，新偏移量: %d\n", newPos)

	buffer2 := make([]byte, 70)
	n, err = rs.Read(buffer2)
	if err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "Seek 后读取错误: %v\n", err)
		return
	}
	fmt.Printf("Seek 后读取 %d 字节 (从偏移量 %d): \"%s...\"\n", n, newPos, string(buffer2[:n]))

	// 3. 再次读取，测试缓存（如果上一次读取的块包含这部分）
	fmt.Println("\n--- 3. 紧接着再次读取 30 字节 (测试缓存) ---")
	// current offset is newPos + n
	buffer3 := make([]byte, 30)
	n, err = rs.Read(buffer3)
	if err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "第三次读取错误: %v\n", err)
	}
	if n > 0 {
		fmt.Printf("第三次读取 %d 字节: \"%s...\"\n", n, string(buffer3[:n]))
	} else if err == io.EOF {
		fmt.Println("第三次读取: 到达文件末尾 (EOF)")
	} else {
		fmt.Printf("第三次读取: 返回 %d 字节，错误: %v\n", n, err)
	}

	// 4. Seek 到文件末尾附近并读取
	if rs.size > 20 {
		seekFromEndOffset := int64(-20)
		fmt.Printf("\n--- 4. Seek 到文件末尾 %d 字节处并尝试读取 50 字节 ---\n", seekFromEndOffset)
		newPos, err = rs.Seek(seekFromEndOffset, io.SeekEnd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "SeekEnd 错误: %v\n", err)
		} else {
			fmt.Printf("SeekEnd 成功，新偏移量: %d\n", newPos)
			buffer4 := make([]byte, 50) // 尝试读取超过文件末尾的字节
			n, err = rs.Read(buffer4)
			if err != nil && err != io.EOF {
				fmt.Fprintf(os.Stderr, "SeekEnd 后读取错误: %v\n", err)
			}
			if n > 0 {
				fmt.Printf("SeekEnd 后读取 %d 字节: \"%s\"\n", n, string(buffer4[:n]))
			}
			if err == io.EOF {
				fmt.Println("SeekEnd 后读取: 到达文件末尾 (EOF) (符合预期)")
			}
		}
	} else {
		fmt.Println("\n--- 4. 文件太小，跳过 SeekEnd 测试 ---")
	}

	// 5. 读取一个之前未缓存的区域
	if rs.size > 300 { // 假设 chunk 是 100，偏移 250 应该在第三个块
		seekOffsetFar := int64(250)
		fmt.Printf("\n--- 5. Seek 到较远偏移量 %d (新块) 并读取 20 字节 ---\n", seekOffsetFar)
		newPos, err = rs.Seek(seekOffsetFar, io.SeekStart)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Seek (far) 错误: %v\n", err)
		} else {
			fmt.Printf("Seek (far) 成功，新偏移量: %d\n", newPos)
			buffer5 := make([]byte, 20)
			n, err = rs.Read(buffer5)
			if err != nil && err != io.EOF {
				fmt.Fprintf(os.Stderr, "Seek (far) 后读取错误: %v\n", err)
			}
			if n > 0 {
				fmt.Printf("Seek (far) 后读取 %d 字节: \"%s...\"\n", n, string(buffer5[:n]))
			}
			if err == io.EOF {
				fmt.Println("Seek (far) 后读取: 到达文件末尾 (EOF)")
			}
		}

	} else {
		fmt.Println("\n--- 5. 文件太小，跳过远距离 Seek 测试 ---")
	}

	fmt.Println("\n测试完成。")
}
