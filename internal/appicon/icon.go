package appicon

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	MaxUploadBytes = 10 << 20
	maxImageSide   = 8192
	maxImagePixels = 40_000_000
	normalizedSize = 1024
)

var androidSizes = map[string]int{
	"mipmap-mdpi":    48,
	"mipmap-hdpi":    72,
	"mipmap-xhdpi":   96,
	"mipmap-xxhdpi":  144,
	"mipmap-xxxhdpi": 192,
}

func Validate(data []byte) error {
	if len(data) == 0 {
		return errors.New("icon upload is empty")
	}
	if len(data) > MaxUploadBytes {
		return errors.New("icon image exceeds the 10 MiB limit")
	}
	_, err := decode(data)
	return err
}

func Generate(sourcePath, outputDir string) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read uploaded icon: %w", err)
	}
	source, err := decode(data)
	if err != nil {
		return err
	}
	square := resizeSquare(source, normalizedSize, false)
	if err := writePNG(filepath.Join(outputDir, "appicon.png"), square); err != nil {
		return err
	}
	if err := writeICO(filepath.Join(outputDir, "windows", "icon.ico"), source); err != nil {
		return err
	}
	for folder, size := range androidSizes {
		dir := filepath.Join(outputDir, "android", folder)
		for _, variant := range []struct {
			name  string
			round bool
		}{{"ic_launcher.png", false}, {"ic_launcher_round.png", true}} {
			icon := resizeSquare(source, size, variant.round)
			if err := writePNG(filepath.Join(dir, variant.name), icon); err != nil {
				return err
			}
		}
	}
	return nil
}

func decode(data []byte) (image.Image, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode uploaded icon: %w", err)
	}
	switch format {
	case "png", "jpeg", "webp":
	default:
		return nil, fmt.Errorf("unsupported icon image format %q; use PNG, JPG, JPEG, or WebP", format)
	}
	if config.Width < 1 || config.Height < 1 ||
		config.Width > maxImageSide || config.Height > maxImageSide ||
		int64(config.Width)*int64(config.Height) > maxImagePixels {
		return nil, fmt.Errorf("icon dimensions must be at most %d by %d and %d megapixels", maxImageSide, maxImageSide, maxImagePixels/1_000_000)
	}
	decoded, actualFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode uploaded icon: %w", err)
	}
	if actualFormat != format || decoded.Bounds().Dx() != config.Width || decoded.Bounds().Dy() != config.Height {
		return nil, errors.New("icon image data is inconsistent or corrupted")
	}
	return decoded, nil
}

type croppedImage struct {
	source image.Image
	bounds image.Rectangle
}

func (c croppedImage) ColorModel() color.Model { return c.source.ColorModel() }
func (c croppedImage) Bounds() image.Rectangle { return image.Rect(0, 0, c.bounds.Dx(), c.bounds.Dy()) }
func (c croppedImage) At(x, y int) color.Color {
	return c.source.At(x+c.bounds.Min.X, y+c.bounds.Min.Y)
}

func resizeSquare(source image.Image, size int, round bool) *image.NRGBA {
	bounds := source.Bounds()
	cropSize := bounds.Dx()
	if bounds.Dy() < cropSize {
		cropSize = bounds.Dy()
	}
	crop := image.Rect(
		bounds.Min.X+(bounds.Dx()-cropSize)/2,
		bounds.Min.Y+(bounds.Dy()-cropSize)/2,
		bounds.Min.X+(bounds.Dx()-cropSize)/2+cropSize,
		bounds.Min.Y+(bounds.Dy()-cropSize)/2+cropSize,
	)
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	cropped := croppedImage{source: source, bounds: crop}
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), cropped, cropped.Bounds(), xdraw.Over, nil)
	if round {
		maskCircle(dst)
	}
	return dst
}

func maskCircle(imageData *image.NRGBA) {
	center := float64(imageData.Bounds().Dx()) / 2
	radius := center
	for y := 0; y < imageData.Bounds().Dy(); y++ {
		for x := 0; x < imageData.Bounds().Dx(); x++ {
			dx, dy := float64(x)+0.5-center, float64(y)+0.5-center
			if dx*dx+dy*dy > radius*radius {
				imageData.SetNRGBA(x, y, color.NRGBA{})
			}
		}
	}
}

func writePNG(path string, imageData image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	encoderErr := png.Encode(file, imageData)
	closeErr := file.Close()
	if encoderErr != nil {
		return encoderErr
	}
	return closeErr
}

func writeICO(path string, source image.Image) error {
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	images := make([][]byte, 0, len(sizes))
	for _, size := range sizes {
		var buffer bytes.Buffer
		if err := png.Encode(&buffer, resizeSquare(source, size, false)); err != nil {
			return fmt.Errorf("encode %dpx Windows icon: %w", size, err)
		}
		images = append(images, buffer.Bytes())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	writeErr := encodeICO(file, sizes, images)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func encodeICO(writer io.Writer, sizes []int, images [][]byte) error {
	if len(sizes) != len(images) || len(sizes) > 0xffff {
		return errors.New("invalid Windows icon image set")
	}
	header := make([]byte, 6+16*len(sizes))
	binary.LittleEndian.PutUint16(header[2:4], 1)
	binary.LittleEndian.PutUint16(header[4:6], uint16(len(sizes)))
	offset := uint32(len(header))
	for index, size := range sizes {
		entry := header[6+16*index : 6+16*(index+1)]
		if size < 256 {
			entry[0], entry[1] = byte(size), byte(size)
		}
		entry[2], entry[3] = 0, 0
		binary.LittleEndian.PutUint16(entry[4:6], 1)
		binary.LittleEndian.PutUint16(entry[6:8], 32)
		binary.LittleEndian.PutUint32(entry[8:12], uint32(len(images[index])))
		binary.LittleEndian.PutUint32(entry[12:16], offset)
		offset += uint32(len(images[index]))
	}
	if _, err := writer.Write(header); err != nil {
		return err
	}
	for _, data := range images {
		if _, err := writer.Write(data); err != nil {
			return err
		}
	}
	return nil
}
