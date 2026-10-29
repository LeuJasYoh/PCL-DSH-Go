// SPDX-License-Identifier: MIT

package safe

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// assertKind 断言错误属于给定的安全归档分类之一。
func assertKind(t *testing.T, err error, kinds ...ErrKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望返回错误，实际 nil")
	}
	got := KindOf(err)
	for _, want := range kinds {
		if got == want {
			return
		}
	}
	t.Fatalf("错误分类 = %q（%v），期望 %v", got, err, kinds)
}

func writeTestFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写入测试文件 %s 失败: %v", path, err)
	}
	return path
}

// zipSample 用于标准库 zip.Writer 生成合法/半恶意 ZIP。
type zipSample struct {
	name   string
	data   []byte
	mode   fs.FileMode
	method uint16
}

func buildZip(t *testing.T, samples []zipSample) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, sample := range samples {
		header := &zip.FileHeader{Name: sample.name, Method: sample.method}
		if sample.mode != 0 {
			header.SetMode(sample.mode)
		}
		entryWriter, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("创建 ZIP 条目 %q 失败: %v", sample.name, err)
		}
		if _, err := entryWriter.Write(sample.data); err != nil {
			t.Fatalf("写入 ZIP 条目 %q 失败: %v", sample.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 ZIP writer 失败: %v", err)
	}
	return writeTestFile(t, t.TempDir(), "sample.zip", buffer.Bytes())
}

// rawZipEntry 用于手写 ZIP（可精确控制原始路径/flags/method/mode/声明大小）。
type rawZipEntry struct {
	name         string
	data         []byte
	method       uint16
	flags        uint16
	mode         uint32 // unix external attrs 高 16 位
	crcOverride  uint32
	uncompressed int64 // <0 表示使用实际长度
	compressed   int64 // <0 表示使用实际长度
}

func newRawZipEntry(name string, data []byte) rawZipEntry {
	return rawZipEntry{name: name, data: data, method: zip.Store, mode: 0o100644, uncompressed: -1, compressed: -1}
}

// buildRawZip 生成一个结构可控的 ZIP 字节流（支持非法名称/flags/method/声明大小）。
func buildRawZip(t *testing.T, entries []rawZipEntry) string {
	t.Helper()
	var body bytes.Buffer
	var central bytes.Buffer
	for _, entry := range entries {
		payload := entry.data
		method := entry.method
		if method == zip.Deflate {
			var compressedBuffer bytes.Buffer
			deflater, err := flate.NewWriter(&compressedBuffer, flate.DefaultCompression)
			if err != nil {
				t.Fatalf("创建 deflate writer 失败: %v", err)
			}
			if _, err := deflater.Write(payload); err != nil {
				t.Fatalf("deflate 压缩失败: %v", err)
			}
			if err := deflater.Close(); err != nil {
				t.Fatalf("关闭 deflate writer 失败: %v", err)
			}
			payload = compressedBuffer.Bytes()
		} else if method == zip.Store {
			payload = append([]byte(nil), entry.data...)
		}
		crc := entry.crcOverride
		if crc == 0 {
			crc = crc32.ChecksumIEEE(entry.data)
		}
		compressedSize := entry.compressed
		if compressedSize < 0 {
			compressedSize = int64(len(payload))
		}
		uncompressedSize := entry.uncompressed
		if uncompressedSize < 0 {
			uncompressedSize = int64(len(entry.data))
		}
		offset := uint32(body.Len())

		var local bytes.Buffer
		local.WriteString("PK\x03\x04")
		writeU16(&local, 20)
		writeU16(&local, entry.flags)
		writeU16(&local, method)
		writeU16(&local, 0) // time
		writeU16(&local, 0) // date
		writeU32(&local, crc)
		writeU32(&local, uint32(compressedSize))
		writeU32(&local, uint32(uncompressedSize))
		writeU16(&local, uint16(len(entry.name)))
		writeU16(&local, 0) // extra len
		local.WriteString(entry.name)
		body.Write(local.Bytes())
		body.Write(payload)

		central.WriteString("PK\x01\x02")
		writeU16(&central, 0x031E) // version made by: unix, 30
		writeU16(&central, 20)     // version needed
		writeU16(&central, entry.flags)
		writeU16(&central, method)
		writeU16(&central, 0)
		writeU16(&central, 0)
		writeU32(&central, crc)
		writeU32(&central, uint32(compressedSize))
		writeU32(&central, uint32(uncompressedSize))
		writeU16(&central, uint16(len(entry.name)))
		writeU16(&central, 0)
		writeU16(&central, 0)
		writeU16(&central, 0)
		writeU16(&central, 0)
		writeU32(&central, entry.mode<<16)
		writeU32(&central, offset)
		central.WriteString(entry.name)
	}
	centralOffset := body.Len()
	body.Write(central.Bytes())
	body.WriteString("PK\x05\x06")
	writeU16(&body, 0)
	writeU16(&body, 0)
	writeU16(&body, uint16(len(entries)))
	writeU16(&body, uint16(len(entries)))
	writeU32(&body, uint32(central.Len()))
	writeU32(&body, uint32(centralOffset))
	writeU16(&body, 0)
	return writeTestFile(t, t.TempDir(), "raw.zip", body.Bytes())
}

func writeU16(buffer *bytes.Buffer, value uint16) {
	_ = binary.Write(buffer, binary.LittleEndian, value)
}

func writeU32(buffer *bytes.Buffer, value uint32) {
	_ = binary.Write(buffer, binary.LittleEndian, value)
}

// tarSample 用于标准库 tar.Writer 生成 tar.gz。
type tarSample struct {
	name     string
	data     []byte
	typeflag byte
	linkname string
	mode     int64
}

// buildTarBytes 生成 tar 字节流（不含 gzip）。
func buildTarBytes(t *testing.T, samples []tarSample) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, sample := range samples {
		typeflag := sample.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name:     sample.name,
			Typeflag: typeflag,
			Mode:     sample.mode,
			Size:     int64(len(sample.data)),
		}
		if header.Mode == 0 {
			header.Mode = 0o644
		}
		if typeflag != tar.TypeReg && typeflag != tar.TypeRegA {
			header.Size = 0
			header.Linkname = sample.linkname
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("写 tar 头 %q 失败: %v", sample.name, err)
		}
		if header.Size > 0 {
			if _, err := writer.Write(sample.data); err != nil {
				t.Fatalf("写 tar 数据 %q 失败: %v", sample.name, err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 tar writer 失败: %v", err)
	}
	return buffer.Bytes()
}

func buildTarGz(t *testing.T, samples []tarSample) string {
	t.Helper()
	return gzipBytes(t, buildTarBytes(t, samples), "sample.tar.gz")
}

func gzipBytes(t *testing.T, raw []byte, filename string) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(raw); err != nil {
		t.Fatalf("gzip 写入失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 gzip writer 失败: %v", err)
	}
	return writeTestFile(t, t.TempDir(), filename, buffer.Bytes())
}

// rawTarHeader 生成一个 512 字节 tar 头（用于构造畸形/稀疏/PAX 等标准库无法写出的样本）。
func rawTarHeader(name string, size int64, typeflag byte, linkname string) []byte {
	header := make([]byte, 512)
	copy(header[0:100], name)
	copy(header[100:108], []byte("0000644\x00"))
	copy(header[108:116], []byte("0000000\x00"))
	copy(header[116:124], []byte("0000000\x00"))
	copy(header[124:136], []byte(fmt.Sprintf("%011o\x00", size)))
	copy(header[136:148], []byte("00000000000\x00"))
	for index := 148; index < 156; index++ {
		header[index] = ' '
	}
	header[156] = typeflag
	copy(header[157:257], linkname)
	copy(header[257:263], []byte("ustar\x00"))
	copy(header[263:265], []byte("00"))
	sum := 0
	for _, value := range header {
		sum += int(value)
	}
	copy(header[148:156], []byte(fmt.Sprintf("%06o\x00 ", sum)))
	return header
}

// buildRawTarGz 用原始数据块拼出 tar.gz（不做任何校验）。
func buildRawTarGz(t *testing.T, blocks ...[]byte) string {
	t.Helper()
	var buffer bytes.Buffer
	for _, block := range blocks {
		buffer.Write(block)
	}
	return gzipBytes(t, buffer.Bytes(), "raw.tar.gz")
}

func zeroBlocks(count int) []byte {
	return make([]byte, count*512)
}

// stepCancelContext 在前 N 次 Err() 调用后返回 context.Canceled，
// 用于确定性地模拟“解包中途取消”。
type stepCancelContext struct {
	remaining int32
}

func (c *stepCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *stepCancelContext) Done() <-chan struct{}       { return nil }
func (c *stepCancelContext) Value(any) any               { return nil }
func (c *stepCancelContext) Err() error {
	if atomic.AddInt32(&c.remaining, -1) < 0 {
		return context.Canceled
	}
	return nil
}
