package main

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

func decodeImageConfig(r io.Reader) (image.Config, string, error) {
	return image.DecodeConfig(r)
}
