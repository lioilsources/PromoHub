package media

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func box(typ string, payload ...[]byte) []byte {
	n := 8
	for _, p := range payload {
		n += len(p)
	}
	b := make([]byte, 8, n)
	binary.BigEndian.PutUint32(b, uint32(n))
	copy(b[4:], typ)
	for _, p := range payload {
		b = append(b, p...)
	}
	return b
}

// synthetic MP4: ftyp, moov{mvhd v0 (timescale 1000, duration 15500), trak{tkhd 1080x1920}}
func TestProbeMP4(t *testing.T) {
	mvhd := make([]byte, 100)
	binary.BigEndian.PutUint32(mvhd[12:], 1000)
	binary.BigEndian.PutUint32(mvhd[16:], 15500)
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd[76:], 1080<<16)
	binary.BigEndian.PutUint32(tkhd[80:], 1920<<16)
	file := append(box("ftyp", []byte("isom0000")), box("moov", box("mvhd", mvhd), box("trak", box("tkhd", tkhd)))...)
	file = append(file, box("mdat", make([]byte, 64))...)
	p := filepath.Join(t.TempDir(), "v.mp4")
	os.WriteFile(p, file, 0o600)
	vi, err := ProbeMP4(p)
	if err != nil || vi.DurationMs != 15500 || vi.Width != 1080 || vi.Height != 1920 {
		t.Fatalf("%+v %v", vi, err)
	}
	os.WriteFile(p, []byte("not a video at all"), 0o600)
	if _, err := ProbeMP4(p); err == nil {
		t.Fatal("garbage must fail")
	}
}
