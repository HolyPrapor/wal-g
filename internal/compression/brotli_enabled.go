//go:build brotli && !windows
// +build brotli,!windows

package compression

import "github.com/wal-g/wal-g/internal/compression/brotli"

func init() {
	Decompressors = append(Decompressors, brotli.Decompressor{})
	Compressors[brotli.AlgorithmName] = brotli.Compressor{}
	CompressingAlgorithms = append(CompressingAlgorithms, brotli.AlgorithmName)
}
