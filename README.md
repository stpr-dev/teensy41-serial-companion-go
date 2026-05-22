# teensy41-serial-companion-go

[![Golangci-lint](https://github.com/stpr-dev/teensy41-serial-companion-go/actions/workflows/lint.yml/badge.svg)](https://github.com/stpr-dev/teensy41-serial-companion-go/actions/workflows/lint.yml)

Go companion code for testing USB serial transfer reliability with a Teensy 4.1.

See [this blog post](https://stpr-dev.github.io/embedded/2026/05/19/teensy-usb-serial-max-speed/) for more information about why this project exists.

For the Teensy firmware, [see this repository](https://github.com/stpr-dev/teensy41-serial-test-firmware).

## Overview

This project sends a handshake to a Teensy 4.1, receives pseudo-random frames over USB serial, and verifies every byte against a locally reproduced JSF32 sequence (seed = 42). It optionally records per-frame timing data for analysis.

## Files

| File                 | Description                                                                                                                  |
|----------------------|------------------------------------------------------------------------------------------------------------------------------|
| `main.go`            | Entry point — parses flags, opens the serial port, sends the handshake, runs the loop, and writes timing output              |
| `loop_sequential.go` | Default loop implementation — reads and verifies frames sequentially (build tag: `!goroutine`)                               |
| `loop_goroutine.go`  | Goroutine-based variant — reads frames in a background goroutine and verifies in the main goroutine (build tag: `goroutine`) |
| `jsf.go`             | JSF32 PRNG, bit-identical to the firmware's C++ implementation                                                               |

## Download

Pre-built binaries for Windows and Linux (amd64) are attached to each [GitHub release](https://github.com/stpr-dev/teensy41-serial-companion-go/releases):

| Platform | File                                           |
|----------|------------------------------------------------|
| Windows  | `serial-companion-<version>-windows-amd64.exe` |
| Linux    | `serial-companion-<version>-linux-amd64`       |

Download the binary for your platform, make it executable if needed (Linux: `chmod +x`), and run it directly — no Go installation required.

## Building from source

Requires Go 1.24+. Install dependencies:

```
go mod download
```

Build a `serial-companion` binary:

```
go build -o serial-companion .
```

## Usage

```
serial-companion --port <port> [options]
```

Or without building first:

```
go run . --port <port> [options]
```

### Arguments

| Argument         | Default    | Description                                                                            |
|------------------|------------|----------------------------------------------------------------------------------------|
| `--port`         | (required) | Serial device path (e.g. `COM3` on Windows, `/dev/ttyACM0` on Linux)                   |
| `--baud`         | `8000000`  | Baud rate. Teensy over USB serial ignores this.                                        |
| `--read-timeout` | `1s`       | Serial read timeout (e.g. `100ms`, `1s`), `0` means no timeout                         |
| `--frame-size`   | `2048`     | Frame size/length in bytes (must be a multiple of 4)                                   |
| `--num-frames`   | `16777216` | Number of frames to receive                                                            |
| `--use-ack`      | off        | Enable ACK mode: send ACK after each frame and retry on failure                        |
| `--timings-dir`  | (none)     | Directory to write per-frame timing `.bin` and `.json` sidecar files; created if absent |

### Build tags

| Tag         | Effect                                                                          |
|-------------|---------------------------------------------------------------------------------|
| *(none)*    | Builds `loop_sequential.go` — simple single-goroutine loop                      |
| `goroutine` | Builds `loop_goroutine.go` — dedicated reader goroutine with a buffered channel |

To use the goroutine variant:

```
go run -tags goroutine . --port COM3
```

### Examples

Stream 1024 frames of 4096 bytes each without ACK:

```
go run . --port COM3 --frame-size 4096 --num-frames 1024
```

Same test with ACK and timing output saved to `timings/`:

```
go run . --port COM3 --frame-size 4096 --num-frames 1024 --use-ack --timings-dir timings
```

## Timing output

When `--timings-dir` is set, two files are written per run:

- **`<timestamp>.bin`** — raw inter-frame intervals as little-endian `int64` nanoseconds (one value per frame interval, so `num_frames - 1` total)
- **`<timestamp>.json`** — metadata sidecar:
  ```json
  {
    "version": 1,
    "dtype": "int64",
    "endian": "little",
    "n_frames": 1023,
    "channels": 1,
    "frame_size": 4096,
    "use_ack": false,
    "start_time": "2026-05-20T12:00:00Z",
    "end_time": "2026-05-20T12:00:05Z",
    "test_duration_s": 5.123
  }
  ```

## Protocol

The companion program initiates each run by writing a 9-byte handshake to the Teensy:

| Bytes | Type          | Description                                          |
|-------|---------------|------------------------------------------------------|
| 0–3   | `uint32_t` LE | Frame size in bytes (multiple of 4)                  |
| 4–7   | `uint32_t` LE | Number of frames                                     |
| 8     | `uint8_t`     | ACK mode: `0x00` = no ACK, any other value = use ACK |

The Teensy then streams `num_frames` frames of `frame_size` bytes each. In ACK mode, the companion sends `0x01` (Ack) after each successfully received frame, or `0x03` (Retransmit) to request a retry (up to 5 retries before aborting).
