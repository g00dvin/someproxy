package speedtest

import (
	"encoding/binary"
	"encoding/json"
	"io"
)

// StreamID is the base reserved MUX stream ID for speed tests.
// IDs from StreamIDBase to StreamIDBase+255 are reserved for parallel streams.
const StreamIDBase uint32 = 0xFFFF0000

// IsSpeedTestStream returns true if the stream ID is in the reserved range.
func IsSpeedTestStream(id uint32) bool {
	return id >= StreamIDBase
}

// Commands
const (
	CmdDownload byte = 0x01
	CmdUpload   byte = 0x02
	CmdPing     byte = 0x03
	CmdResult   byte = 0x04
	CmdData     byte = 0x05
	CmdDone     byte = 0x06
)

// HeaderSize is 5 bytes: [cmd(1) + length(4)]
const HeaderSize = 5

func writeHeader(w io.Writer, cmd byte, length uint32) error {
	var buf [HeaderSize]byte
	buf[0] = cmd
	binary.BigEndian.PutUint32(buf[1:], length)
	_, err := w.Write(buf[:])
	return err
}

func readHeader(r io.Reader) (cmd byte, length uint32, err error) {
	var buf [HeaderSize]byte
	if _, err = io.ReadFull(r, buf[:]); err != nil {
		return
	}
	cmd = buf[0]
	length = binary.BigEndian.Uint32(buf[1:])
	return
}

// Result is the final speed test output.
type Result struct {
	Ping        PingResult     `json:"ping"`
	Download    TransferResult `json:"download"`
	Upload      TransferResult `json:"upload"`
	Connections []ConnResult   `json:"connections"`
}

type PingResult struct {
	MinMs    float64 `json:"min_ms"`
	AvgMs    float64 `json:"avg_ms"`
	MaxMs    float64 `json:"max_ms"`
	JitterMs float64 `json:"jitter_ms"`
}

type TransferResult struct {
	Bytes     int64   `json:"bytes"`
	DurationS float64 `json:"duration_s"`
	Mbps      float64 `json:"mbps"`
}

type ConnResult struct {
	Index     int     `json:"index"`
	DownBytes int64   `json:"down_bytes"`
	UpBytes   int64   `json:"up_bytes"`
	DownMbps  float64 `json:"down_mbps"`
	UpMbps    float64 `json:"up_mbps"`
	LatencyMs float64 `json:"latency_ms"`
}

// Progress is emitted every second during a phase.
type Progress struct {
	Phase       string  `json:"phase"`
	ElapsedS    int     `json:"elapsed_s"`
	CurrentMbps float64 `json:"current_mbps"`
	Bytes       int64   `json:"bytes"`
}

// Callback receives streaming updates.
type Callback interface {
	OnPhase(phase string)
	OnProgress(jsonData string)
	OnComplete(jsonData string)
	OnError(err string)
}

// JSON returns the result as a JSON string.
func (r *Result) JSON() string {
	b, _ := json.Marshal(r)
	return string(b)
}
