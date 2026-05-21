//go:build goroutine

package main

import (
	"bufio"
	"encoding/binary"
	"log"
	"sync"
	"time"

	"go.bug.st/serial"
)

const (
	Ack        = 0x01
	Retransmit = 0x03
	maxRetry   = 5
)

func readLoop(port serial.Port, numSamples uint32, pool *sync.Pool, results chan<- []byte, useAck bool) {
	//runtime.LockOSThread()
	//defer runtime.UnlockOSThread()
	//
	//if err := windows.SetPriorityClass(windows.CurrentProcess(), windows.HIGH_PRIORITY_CLASS); err != nil {
	//	log.Fatalf("Failed to set high priority class: %v", err)
	//}
	log.Println("Running Go routine-based acquisition.")
	for i := range numSamples {
		buf := pool.Get().([]byte)
		if !useAck {
			if err := mustReadFull(port, buf); err != nil {
				log.Fatalf("[Sample %v] Failed to read full frame: %v", i, err)
			}
		} else {
			if err := mustReadFullWithRetry(port, buf, maxRetry, Retransmit); err != nil {
				log.Fatalf("[Sample %v] Failed to read full frame after %d retries: %v", i, maxRetry, err)
			}
			if err := writeAck(port, Ack); err != nil {
				log.Fatalf("[Sample %v] Failed to write ACK after %d retries: %v", i, maxRetry, err)
			}
		}
		results <- buf
	}
}

func runLoop(port serial.Port, numFrames, frameSize uint32, useAck bool, timingsWriter *bufio.Writer, jsf *JSF32) loopResult {
	const trimSamples = 10
	var prevTime time.Time
	var res loopResult

	pool := &sync.Pool{New: func() any { return make([]byte, frameSize) }}
	const writerQueueSize = 1 << 16
	results := make(chan []byte, writerQueueSize)

	res.StartTime = time.Now()
	go readLoop(port, numFrames, pool, results, useAck)
	for i := range numFrames {
		buf := <-results
		now := time.Now()

		if i > 0 {
			diff := now.Sub(prevTime).Nanoseconds()
			if timingsWriter != nil {
				var b [8]byte
				binary.LittleEndian.PutUint64(b[:], uint64(diff))
				if _, err := timingsWriter.Write(b[:]); err != nil {
					log.Printf("Failed to write to timings file: %v", err)
				}
			}

			if i > trimSamples {
				if res.CountNs == 0 {
					res.MinNs = diff
					res.MaxNs = diff
					res.SumNs = diff
					res.CountNs = 1
				} else {
					if diff < res.MinNs {
						res.MinNs = diff
					}
					if diff > res.MaxNs {
						res.MaxNs = diff
					}
					res.SumNs += diff
					res.CountNs++
				}
			}
		}
		prevTime = now

		for j := 0; j < len(buf)/4; j++ {
			received := binary.LittleEndian.Uint32(buf[j*4:])
			expected := jsf.Next()
			if received != expected {
				log.Fatalf("[Frame (%v), Value (%v)] Write failed: Received %v value instead of expected %v", i, j, received, expected)
			}
		}
		pool.Put(buf)
	}
	res.EndTime = time.Now()
	return res
}
