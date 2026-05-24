package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"time"

	"go.bug.st/serial"
)

type loopResult struct {
	StartTime time.Time
	EndTime   time.Time
	MinNs     int64
	MaxNs     int64
	SumNs     int64
	CountNs   int64
}

func mustReadFull(port serial.Port, buf []byte) error {
	totalRead := 0
	for totalRead < len(buf) {
		n, err := port.Read(buf[totalRead:])
		if err != nil {
			return fmt.Errorf("read error after %d bytes: %w", totalRead, err)
		}
		if n == 0 {
			return fmt.Errorf("read returned 0 bytes after %d/%d", totalRead, len(buf))
		}
		totalRead += n
	}
	return nil
}

func mustReadFullWithRetry(port serial.Port, buf []byte, numRetries int, retryByte byte) error {
	for range numRetries {
		err := mustReadFull(port, buf)
		if err != nil {
			clearBuffers(port)
			if err := writeAck(port, retryByte); err != nil {
				return err
			}
			log.Printf("Retrying read after error: %v", err)
			continue
		} else {
			return nil
		}
	}
	return fmt.Errorf("read failed after %d retries", numRetries)
}

func writeBytes(port serial.Port, buf []byte) error {
	if len(buf) == 0 {
		return nil
	}
	remaining := len(buf)
	for remaining > 0 {
		n, err := port.Write(buf)
		if err != nil {
			return fmt.Errorf("write error: %w", err)
		}
		remaining -= n
		buf = buf[n:]
	}
	return nil
}

func writeAck(port serial.Port, ackByte byte) error {
	if ackByte == 0 {
		return nil // No ACK if ACK is 0x00
	}
	return writeBytes(port, []byte{ackByte})
}

func deferClose(c io.Closer, name string) {
	if err := c.Close(); err != nil {
		log.Printf("Failed to close %s: %v", name, err)
	}
}

func clearBuffers(port serial.Port) {
	if err := port.ResetOutputBuffer(); err != nil {
		log.Printf("Failed to reset output buffer: %v", err)
	}
	if err := port.ResetInputBuffer(); err != nil {
		log.Printf("Failed to reset input buffer: %v", err)
	}
}

func main() {
	flagPort := flag.String("port", "", "serial device path (required)")
	flagBaud := flag.Uint64("baud", 8000000, "baud rate")
	flagReadTimeout := flag.Duration("read-timeout", 1*time.Second, "serial read timeout (e.g. 100ms, 1s), 0 means no timeout")
	flagFrameSize := flag.Uint64("frame-size", 2048, "serial read frame size in bytes per sample")
	flagNumFrames := flag.Uint64("num-frames", 1<<24, "number of samples to transmit")
	flagAck := flag.Bool("use-ack", false, "ACK for each frame and retry for failures.")
	flagTimingsDir := flag.String("timings-dir", "", "output directory for .bin and .json sidecar timing files; created if absent (optional)")

	flag.Parse()

	if *flagPort == "" {
		log.Fatal("Flag --port is required and cannot be empty")
	}
	if *flagFrameSize > math.MaxUint32 {
		log.Fatalf("Flag --frame-size must be less than %d", math.MaxUint32)
	}
	if *flagNumFrames > math.MaxUint32 {
		log.Fatalf("Flag --num-frames must be less than %d", math.MaxUint32)
	}
	if *flagFrameSize%4 != 0 {
		log.Fatalf("Flag --frame-size must be a multiple of 4")
	}

	var jsf = NewJSF32(42)

	var serialPort = *flagPort
	var baudRate = int(*flagBaud)
	var useAck = *flagAck
	var frameSize = uint32(*flagFrameSize)
	var numFrames = uint32(*flagNumFrames)

	mode := &serial.Mode{BaudRate: baudRate}
	port, err := serial.Open(serialPort, mode)
	if err != nil {
		log.Fatalf("Failed to open port: %v", err)
	}
	defer deferClose(port, "serial")
	clearBuffers(port)

	if *flagReadTimeout == time.Duration(0) {
		if err := port.SetReadTimeout(serial.NoTimeout); err != nil {
			log.Fatalf("Failed to set timeout: %v", err)
		}
	} else {
		if err := port.SetReadTimeout(*flagReadTimeout); err != nil {
			log.Fatalf("Failed to set timeout: %v", err)
		}
	}

	fmt.Printf("Port %s opened.\n", serialPort)

	// Handshake: [4B frame size, 4B numTransmits, 1B ackByte]
	handshakeData := make([]byte, 9)
	binary.LittleEndian.PutUint32(handshakeData, frameSize)
	binary.LittleEndian.PutUint32(handshakeData[4:], numFrames)
	ackByte := 0x00
	if useAck {
		ackByte = 0x01
	}
	handshakeData[8] = byte(ackByte)

	if err := writeBytes(port, handshakeData); err != nil {
		log.Fatalf("Failed to send handshake data: %v", err)
	}

	fmt.Printf("Sent handshake data:\n%v frame size, %v frames, and using ack: %v. Current time is %s. Waiting for data...\n", frameSize, numFrames, useAck, time.Now().Format(time.RFC3339))

	var timingsBasePath string
	var timingsFile *os.File
	var timingsWriter *bufio.Writer
	if *flagTimingsDir != "" {
		if err := os.MkdirAll(*flagTimingsDir, 0o755); err != nil {
			log.Fatalf("Failed to create timings directory: %v", err)
		}
		basename := time.Now().Format("20060102_150405")
		timingsBasePath = filepath.Join(*flagTimingsDir, basename)
		f, err := os.Create(timingsBasePath + ".bin")
		if err != nil {
			log.Fatalf("Failed to create timings file: %v", err)
		}
		timingsFile = f
		timingsWriter = bufio.NewWriter(f)
	}

	res := runLoop(port, numFrames, frameSize, useAck, timingsWriter, jsf)

	fmt.Printf("All data received. Current time: %s\n", res.EndTime.Format(time.RFC3339))

	elapsed := res.EndTime.Sub(res.StartTime)
	fmt.Printf("Elapsed time for the entire test: %s\n", elapsed)

	if res.CountNs > 0 {
		meanNs := float64(res.SumNs) / float64(res.CountNs)
		fmt.Printf("Min delay:  %.2f ns\n", float64(res.MinNs))
		fmt.Printf("Max delay:  %.2f ns\n", float64(res.MaxNs))
		fmt.Printf("Mean delay: %.2f ns\n", meanNs)
	} else {
		fmt.Println("Not enough samples for timing statistics.")
	}

	if timingsWriter != nil {
		if err := timingsWriter.Flush(); err != nil {
			log.Fatalf("Failed to flush timings file: %v", err)
		}
		if err := timingsFile.Close(); err != nil {
			log.Fatalf("Failed to close timings file: %v", err)
		}

		// Integrity: reader should verify n_frames * channels * 8 == file size in bytes.
		// n_frames is numFrames-1 because the first frame has no preceding timestamp.
		type sidecar struct {
			Version      int     `json:"version"`
			Dtype        string  `json:"dtype"`
			Endian       string  `json:"endian"`
			NFrames      uint64  `json:"n_frames"`
			Channels     int     `json:"channels"`
			FrameSize    uint32  `json:"frame_size"`
			UseAck       bool    `json:"use_ack"`
			StartTime    string  `json:"start_time"`
			EndTime      string  `json:"end_time"`
			TestDuration float64 `json:"test_duration_s"`
		}
		meta := sidecar{
			Version:      1,
			Dtype:        "uint64",
			Endian:       "little",
			NFrames:      uint64(numFrames) - 1,
			Channels:     1,
			FrameSize:    frameSize,
			UseAck:       useAck,
			StartTime:    res.StartTime.UTC().Format(time.RFC3339),
			EndTime:      res.EndTime.UTC().Format(time.RFC3339),
			TestDuration: elapsed.Seconds(),
		}
		jsonBytes, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			log.Fatalf("Failed to marshal JSON sidecar: %v", err)
		}
		if err := os.WriteFile(timingsBasePath+".json", append(jsonBytes, '\n'), 0o644); err != nil {
			log.Fatalf("Failed to write JSON sidecar: %v", err)
		}
	}
}
