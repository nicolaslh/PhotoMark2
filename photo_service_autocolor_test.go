package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestAutoColorImageFromDataURL(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 12, 8))
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			source.SetNRGBA(x, y, color.NRGBA{
				R: uint8(80 + x*8),
				G: uint8(55 + y*10),
				B: uint8(35 + x*3),
				A: 255,
			})
		}
	}

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())

	result, err := (&PhotoService{}).AutoColorImage(dataURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(result.Path) })

	if !strings.HasPrefix(result.DataURL, "data:image/png;base64,") {
		t.Fatalf("unexpected data URL prefix: %.32q", result.DataURL)
	}
	if result.Report.GreenGain != 1 {
		t.Fatalf("green gain = %v; want 1", result.Report.GreenGain)
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("cached result is unavailable: %v", err)
	}
}

func TestDecodeAutoColorSourceRejectsInvalidDataURL(t *testing.T) {
	if _, err := decodeAutoColorSource("data:image/png,not-base64"); err == nil {
		t.Fatal("decodeAutoColorSource accepted a non-base64 data URL")
	}
}
