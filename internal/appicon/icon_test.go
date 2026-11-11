package appicon

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSupportedImageFormats(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "PNG", data: encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 2, 2)))},
		{name: "JPEG", data: encodeJPEG(t)},
		{name: "WebP", data: decodeBase64(t, "UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA==")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(test.data); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateRejectsCorruptUnsupportedOversizedAndHugeImages(t *testing.T) {
	largeDimensions := image.NewNRGBA(image.Rect(0, 0, maxImageSide+1, 1))
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "corrupt", data: []byte("not an image"), want: "decode uploaded icon"},
		{name: "unsupported", data: encodeGIF(t), want: "unsupported icon image format"},
		{name: "oversized upload", data: bytes.Repeat([]byte{0}, MaxUploadBytes+1), want: "10 MiB"},
		{name: "huge dimensions", data: encodePNG(t, largeDimensions), want: "dimensions"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.data)
			if err == nil || !bytes.Contains([]byte(err.Error()), []byte(test.want)) {
				t.Fatalf("Validate() error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestGenerateCreatesAndroidAndWindowsIcons(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		source.SetNRGBA(0, y, color.NRGBA{B: 255, A: 255})
		source.SetNRGBA(1, y, color.NRGBA{R: 255, A: 128})
		source.SetNRGBA(2, y, color.NRGBA{R: 255, A: 128})
		source.SetNRGBA(3, y, color.NRGBA{B: 255, A: 255})
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "upload")
	if err := os.WriteFile(input, encodePNG(t, source), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "generated")
	if err := Generate(input, output); err != nil {
		t.Fatal(err)
	}
	appIcon := decodePNG(t, filepath.Join(output, "appicon.png"))
	if appIcon.Bounds().Dx() != normalizedSize || appIcon.Bounds().Dy() != normalizedSize {
		t.Fatalf("appicon dimensions = %v", appIcon.Bounds())
	}
	red, _, blue, alpha := appIcon.At(normalizedSize/2, normalizedSize/2).RGBA()
	if red <= blue || alpha == 0 || alpha >= 0xffff {
		t.Fatalf("center pixel red/blue/alpha = %d/%d/%d; want centered crop and preserved transparency", red, blue, alpha)
	}
	for folder, size := range androidSizes {
		for _, name := range []string{"ic_launcher.png", "ic_launcher_round.png"} {
			icon := decodePNG(t, filepath.Join(output, "android", folder, name))
			if icon.Bounds().Dx() != size || icon.Bounds().Dy() != size {
				t.Fatalf("%s/%s dimensions = %v", folder, name, icon.Bounds())
			}
		}
	}
	round := decodePNG(t, filepath.Join(output, "android", "mipmap-mdpi", "ic_launcher_round.png"))
	if _, _, _, alpha := round.At(0, 0).RGBA(); alpha != 0 {
		t.Fatalf("round icon corner alpha = %d, want transparent", alpha)
	}
	ico, err := os.ReadFile(filepath.Join(output, "windows", "icon.ico"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ico) < 6 || binary.LittleEndian.Uint16(ico[2:4]) != 1 || binary.LittleEndian.Uint16(ico[4:6]) != 7 {
		t.Fatalf("invalid ICO header: %v", ico[:min(6, len(ico))])
	}
	for index := 0; index < 7; index++ {
		entry := ico[6+16*index : 6+16*(index+1)]
		size := int(entry[0])
		if size == 0 {
			size = 256
		}
		dataSize := int(binary.LittleEndian.Uint32(entry[8:12]))
		offset := int(binary.LittleEndian.Uint32(entry[12:16]))
		frame, format, err := image.Decode(bytes.NewReader(ico[offset : offset+dataSize]))
		if err != nil || format != "png" || frame.Bounds().Dx() != size || frame.Bounds().Dy() != size {
			t.Fatalf("invalid %dpx ICO frame: format=%q bounds=%v error=%v", size, format, frameBounds(frame), err)
		}
	}
}

func encodePNG(t *testing.T, source image.Image) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, source); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func encodeJPEG(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 3, 2)), nil); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func encodeGIF(t *testing.T) []byte {
	t.Helper()
	source := image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black})
	var data bytes.Buffer
	if err := gif.Encode(&data, source, nil); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func decodePNG(t *testing.T, path string) image.Image {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoded, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func decodeBase64(t *testing.T, value string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func frameBounds(frame image.Image) image.Rectangle {
	if frame == nil {
		return image.Rectangle{}
	}
	return frame.Bounds()
}
