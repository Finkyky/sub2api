//go:build !js

package websocket

import (
	"bytes"
	"compress/flate"
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type observedCompressionFlush struct {
	compressionWriter
	flushed chan struct{}
}

func (writer observedCompressionFlush) Flush() error {
	err := writer.compressionWriter.Flush()
	close(writer.flushed)
	return err
}

func TestCodexCancelledCompressedSendCannotReuseConnection(t *testing.T) {
	clientWire, serverWire := net.Pipe()
	defer serverWire.Close()
	client := newConn(connConfig{client: true, rwc: clientWire,
		br: getBufioReader(clientWire), bw: getBufioWriter(clientWire), flateThreshold: 1,
		copts: &compressionOptions{clientMaxWindowBits: true, clientWindowBits: 9, singleFrameMessages: true},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, client.writeFrameMu.lock(ctx))
	locked := true
	defer func() {
		if locked {
			client.writeFrameMu.unlock()
		}
		_ = client.CloseNow()
	}()
	flushed := make(chan struct{})
	client.msgWriter.trimWriter = &trimLastFourBytesWriter{}
	client.msgWriter.flateWriter = observedCompressionFlush{compressionWriter: getWindowedFlateWriter(client.msgWriter.trimWriter, 9), flushed: flushed}
	finished := make(chan error, 1)
	go func() {
		finished <- client.Write(ctx, MessageText, bytes.Repeat([]byte("cancelled context dictionary"), 100))
	}()
	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Fatal("compressor did not flush")
	}
	cancel()
	select {
	case <-client.closed:
	case <-time.After(time.Second):
		t.Error("connection remained reusable after unsent compressed data advanced its dictionary")
	}
	client.writeFrameMu.unlock()
	locked = false
	require.ErrorIs(t, <-finished, context.Canceled)
}

func TestCodexCancelledSendKeepsMessageLockUntilClosed(t *testing.T) {
	clientWire, serverWire := net.Pipe()
	defer serverWire.Close()
	client := newConn(connConfig{client: true, rwc: clientWire,
		br: getBufioReader(clientWire), bw: getBufioWriter(clientWire), flateThreshold: 1,
		copts: &compressionOptions{clientMaxWindowBits: true, clientWindowBits: 9, singleFrameMessages: true},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, client.writeFrameMu.lock(ctx))
	client.closeMu.Lock()
	locksHeld := true
	defer func() {
		if locksHeld {
			client.writeFrameMu.unlock()
			client.closeMu.Unlock()
		}
		_ = client.CloseNow()
	}()
	flushed := make(chan struct{})
	client.msgWriter.trimWriter = &trimLastFourBytesWriter{}
	client.msgWriter.flateWriter = observedCompressionFlush{compressionWriter: getWindowedFlateWriter(client.msgWriter.trimWriter, 9), flushed: flushed}
	finished := make(chan error, 1)
	go func() {
		finished <- client.Write(ctx, MessageText, bytes.Repeat([]byte("cancelled context dictionary"), 100))
	}()
	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Fatal("compressor did not flush")
	}
	cancel()
	waitCtx, stopWaiting := context.WithTimeout(context.Background(), time.Second)
	defer stopWaiting()
	require.NoError(t, client.msgWriter.writeMu.lock(waitCtx))
	client.msgWriter.writeMu.unlock()
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if client.msgWriter.mu.tryLock() {
			client.msgWriter.mu.unlock()
			t.Error("another writer can enter before the failed compressed connection is closed")
			break
		}
		runtime.Gosched()
	}
	client.writeFrameMu.unlock()
	client.closeMu.Unlock()
	locksHeld = false
	require.ErrorIs(t, <-finished, context.Canceled)
}

func TestCodexCompressionNegotiation(t *testing.T) {
	for bits := 9; bits <= 15; bits++ {
		for _, quoted := range []bool{false, true} {
			value := fmt.Sprint(bits)
			if quoted {
				value = `"` + value + `"`
			}
			extension := "permessage-deflate; client_max_window_bits=" + value + "; server_max_window_bits=15; client_no_context_takeover"
			options, err := verifyServerExtensions(&compressionOptions{clientMaxWindowBits: true, clientWindowBits: 15}, http.Header{"Sec-Websocket-Extensions": {extension}})
			require.NoError(t, err)
			require.Equal(t, bits, options.clientWindowBits)
			require.True(t, options.clientNoContextTakeover)
		}
	}
	for _, extension := range []string{
		"permessage-deflate; client_max_window_bits=8",
		"permessage-deflate; server_max_window_bits=8",
		"permessage-deflate; client_max_window_bits=7",
		"permessage-deflate; client_max_window_bits=16",
		"permessage-deflate; client_max_window_bits=9; client_max_window_bits=10",
		"permessage-deflate; server_max_window_bits=16",
		"permessage-deflate; client_no_context_takeover=true",
		"permessage-deflate; server_no_context_takeover=true",
	} {
		_, err := verifyServerExtensions(&compressionOptions{clientMaxWindowBits: true, clientWindowBits: 15}, http.Header{"Sec-Websocket-Extensions": {extension}})
		require.Error(t, err, extension)
	}
	_, err := verifyServerExtensions(&compressionOptions{}, http.Header{"Sec-Websocket-Extensions": {"permessage-deflate; client_max_window_bits=9"}})
	require.Error(t, err)
	options, err := verifyServerExtensions(&compressionOptions{clientMaxWindowBits: true, clientWindowBits: 15}, http.Header{"Sec-Websocket-Extensions": {"permessage-deflate"}})
	require.NoError(t, err)
	require.Equal(t, 15, options.clientWindowBits)
	options, err = verifyServerExtensions(&compressionOptions{clientMaxWindowBits: true, clientWindowBits: 15}, http.Header{"Sec-Websocket-Extensions": {"permessage-deflate; client_max_window_bits"}})
	require.NoError(t, err)
	require.Equal(t, 15, options.clientWindowBits)
	options, err = verifyServerExtensions(&compressionOptions{clientMaxWindowBits: true, clientWindowBits: 15}, http.Header{})
	require.NoError(t, err)
	require.Nil(t, options)
}

func TestCodexCompressorHonorsNegotiatedWindowAndContext(t *testing.T) {
	random := rand.New(rand.NewPCG(41, 73))
	block := make([]byte, 2048)
	for index := range block {
		block[index] = byte(random.IntN(256))
	}
	payload := append(bytes.Repeat(block, 8), bytes.Repeat([]byte("Codex window check "), 500)...)
	for bits := 8; bits <= 15; bits++ {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			for _, takeover := range []bool{false, true} {
				var output bytes.Buffer
				trimmed := &trimLastFourBytesWriter{w: &output}
				var compressor compressionWriter
				var dictionary []byte
				for message := 0; message < 3; message++ {
					output.Reset()
					trimmed.reset()
					if compressor == nil || !takeover {
						compressor = getWindowedFlateWriter(trimmed, bits)
						dictionary = nil
					}
					_, err := compressor.Write(payload)
					require.NoError(t, err)
					require.NoError(t, compressor.Flush())
					wire := append(append([]byte(nil), output.Bytes()...), 0, 0, 255, 255, 1, 0, 0, 255, 255)
					distance := maximumDeflateDistance(t, wire)
					require.LessOrEqual(t, distance, 1<<bits, "compressor must implement, not just advertise, the negotiated window")
					if bits == 15 {
						require.Greater(t, distance, 512, "payload must exercise long-distance references")
					}
					decoder := flate.NewReaderDict(bytes.NewReader(wire), dictionary)
					decoded, err := io.ReadAll(decoder)
					require.NoError(t, err)
					require.NoError(t, decoder.Close())
					require.Equal(t, payload, decoded)
					dictionary = append(dictionary, decoded...)
					if len(dictionary) > 1<<bits {
						dictionary = dictionary[len(dictionary)-(1<<bits):]
					}
				}
			}
		})
	}
}

type deflateTestBits struct {
	data   []byte
	offset int
}

func (reader *deflateTestBits) read(t *testing.T, count int) int {
	t.Helper()
	require.LessOrEqual(t, reader.offset+count, len(reader.data)*8)
	value := 0
	for bit := 0; bit < count; bit++ {
		value |= int((reader.data[reader.offset/8]>>(reader.offset%8))&1) << bit
		reader.offset++
	}
	return value
}

func deflateTestCodes(lengths []int) map[int]int {
	counts := make([]int, 16)
	for _, length := range lengths {
		if length > 0 {
			counts[length]++
		}
	}
	next := make([]int, 16)
	code := 0
	for bits := 1; bits < 16; bits++ {
		code = (code + counts[bits-1]) << 1
		next[bits] = code
	}
	codes := make(map[int]int)
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		value := next[length]
		next[length]++
		reversed := 0
		for bit := 0; bit < length; bit++ {
			reversed = (reversed << 1) | ((value >> bit) & 1)
		}
		codes[length<<16|reversed] = symbol
	}
	return codes
}

func (reader *deflateTestBits) symbol(t *testing.T, codes map[int]int) int {
	t.Helper()
	value := 0
	for length := 1; length <= 15; length++ {
		value |= reader.read(t, 1) << (length - 1)
		if symbol, ok := codes[length<<16|value]; ok {
			return symbol
		}
	}
	t.Fatal("invalid deflate Huffman code")
	return 0
}

func maximumDeflateDistance(t *testing.T, data []byte) int {
	t.Helper()
	reader := deflateTestBits{data: data}
	maximum := 0
	lengthExtra := []int{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
	distanceBase := []int{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193, 257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}
	distanceExtra := []int{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13}
	for {
		final := reader.read(t, 1) != 0
		blockType := reader.read(t, 2)
		if blockType == 0 {
			reader.offset = (reader.offset + 7) / 8 * 8
			length := reader.read(t, 16)
			require.Equal(t, length^65535, reader.read(t, 16))
			reader.offset += length * 8
			require.LessOrEqual(t, reader.offset, len(data)*8)
		} else {
			var literals, distances []int
			if blockType == 1 {
				literals = make([]int, 288)
				for index := range literals {
					switch {
					case index < 144:
						literals[index] = 8
					case index < 256:
						literals[index] = 9
					case index < 280:
						literals[index] = 7
					default:
						literals[index] = 8
					}
				}
				distances = make([]int, 32)
				for index := range distances {
					distances[index] = 5
				}
			} else {
				require.Equal(t, 2, blockType)
				literalCount := reader.read(t, 5) + 257
				distanceCount := reader.read(t, 5) + 1
				codeCount := reader.read(t, 4) + 4
				order := []int{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}
				codeLengths := make([]int, 19)
				for index := 0; index < codeCount; index++ {
					codeLengths[order[index]] = reader.read(t, 3)
				}
				codes := deflateTestCodes(codeLengths)
				var lengths []int
				for len(lengths) < literalCount+distanceCount {
					symbol := reader.symbol(t, codes)
					if symbol < 16 {
						lengths = append(lengths, symbol)
						continue
					}
					value, repeat := 0, 0
					switch symbol {
					case 16:
						require.NotEmpty(t, lengths)
						value = lengths[len(lengths)-1]
						repeat = reader.read(t, 2) + 3
					case 17:
						repeat = reader.read(t, 3) + 3
					case 18:
						repeat = reader.read(t, 7) + 11
					}
					for range repeat {
						lengths = append(lengths, value)
					}
				}
				require.Len(t, lengths, literalCount+distanceCount)
				literals, distances = lengths[:literalCount], lengths[literalCount:]
			}
			literalCodes, distanceCodes := deflateTestCodes(literals), deflateTestCodes(distances)
			for {
				symbol := reader.symbol(t, literalCodes)
				if symbol == 256 {
					break
				}
				if symbol < 256 {
					continue
				}
				require.LessOrEqual(t, symbol, 285)
				reader.read(t, lengthExtra[symbol-257])
				distance := reader.symbol(t, distanceCodes)
				require.Less(t, distance, len(distanceBase))
				actual := distanceBase[distance] + reader.read(t, distanceExtra[distance])
				if actual > maximum {
					maximum = actual
				}
			}
		}
		if final {
			return maximum
		}
	}
}
