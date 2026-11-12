package media

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"testing"
)

func TestSavePNG(t *testing.T) {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 30, 20)))
	dir := t.TempDir()
	s, err := Save(dir, bytes.NewReader(buf.Bytes()), "shot.png")
	if err != nil {
		t.Fatal(err)
	}
	if s.Mime != "image/png" || s.Width != 30 || s.Height != 20 || s.Bytes != int64(buf.Len()) {
		t.Fatalf("%+v", s)
	}
	if _, err := os.Stat(s.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(dir, bytes.NewReader(nil), "x.png"); err == nil {
		t.Fatal("empty file must fail")
	}
	if a, b := Token(), Token(); a == b || len(a) != 43 {
		t.Fatalf("token %q %q", a, b)
	}
}
