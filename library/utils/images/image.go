package images

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
)

// VideoFrame represents a raw video frame from the WebRTC track
type VideoFrame struct {
	Data   []byte
	Width  int
	Height int
	Format string // e.g., "yuv420p", "rgb24", "rgba", "argb"
}

type EncodeOptions struct {
	Format   string // "jpeg" or "png"
	Quality  int    // 1-100 for jpeg
	Width    int    // 0 means original
	Height   int    // 0 means original
	Strategy string // "center_aspect_fit", "center_aspect_cover", "scale_aspect_fit", "scale_aspect_cover", "skew"
}

func NewEncodeOptions() EncodeOptions {
	return EncodeOptions{
		Format:   "jpeg",
		Quality:  85,
		Width:    0,
		Height:   0,
		Strategy: "scale_aspect_fit", // Default behavior
	}
}

// Encode resizes (if needed) and encodes a raw VideoFrame into a compressed byte slice (JPEG/PNG).
func Encode(frame *VideoFrame, opts EncodeOptions) ([]byte, error) {
	if frame == nil || len(frame.Data) == 0 {
		return nil, fmt.Errorf("empty video frame")
	}

	var img image.Image
	rect := image.Rect(0, 0, frame.Width, frame.Height)

	switch frame.Format {
	case "yuv420p":
		ySize := frame.Width * frame.Height
		cSize := (frame.Width / 2) * (frame.Height / 2)
		if len(frame.Data) < ySize+2*cSize {
			return nil, fmt.Errorf("insufficient data for yuv420p frame: expected %d, got %d", ySize+2*cSize, len(frame.Data))
		}
		
		yuvImg := image.NewYCbCr(rect, image.YCbCrSubsampleRatio420)
		copy(yuvImg.Y, frame.Data[:ySize])
		copy(yuvImg.Cb, frame.Data[ySize:ySize+cSize])
		copy(yuvImg.Cr, frame.Data[ySize+cSize:ySize+2*cSize])
		img = yuvImg

	case "rgba":
		expected := frame.Width * frame.Height * 4
		if len(frame.Data) < expected {
			return nil, fmt.Errorf("insufficient data for rgba frame")
		}
		rgbaImg := image.NewRGBA(rect)
		rgbaImg.Pix = frame.Data[:expected]
		img = rgbaImg

	case "argb":
		expected := frame.Width * frame.Height * 4
		if len(frame.Data) < expected {
			return nil, fmt.Errorf("insufficient data for argb frame")
		}
		rgbaImg := image.NewRGBA(rect)
		// Convert ARGB to RGBA
		for i := 0; i < expected; i += 4 {
			rgbaImg.Pix[i] = frame.Data[i+1]   // R
			rgbaImg.Pix[i+1] = frame.Data[i+2] // G
			rgbaImg.Pix[i+2] = frame.Data[i+3] // B
			rgbaImg.Pix[i+3] = frame.Data[i]   // A
		}
		img = rgbaImg

	case "rgb24":
		expected := frame.Width * frame.Height * 3
		if len(frame.Data) < expected {
			return nil, fmt.Errorf("insufficient data for rgb24 frame")
		}
		rgbaImg := image.NewRGBA(rect)
		for i, j := 0, 0; i < expected; i, j = i+3, j+4 {
			rgbaImg.Pix[j] = frame.Data[i]     // R
			rgbaImg.Pix[j+1] = frame.Data[i+1] // G
			rgbaImg.Pix[j+2] = frame.Data[i+2] // B
			rgbaImg.Pix[j+3] = 255             // A
		}
		img = rgbaImg

	default:
		return nil, fmt.Errorf("unsupported or unknown video frame format: %s", frame.Format)
	}

	// Resizing logic
	if (opts.Width > 0 && opts.Width != frame.Width) || (opts.Height > 0 && opts.Height != frame.Height) {
		if opts.Width == 0 {
			opts.Width = frame.Width
		}
		if opts.Height == 0 {
			opts.Height = frame.Height
		}
		if opts.Strategy == "" {
			opts.Strategy = "scale_aspect_fit"
		}

		origWidth := float64(frame.Width)
		origHeight := float64(frame.Height)
		targetWidth := float64(opts.Width)
		targetHeight := float64(opts.Height)

		var newWidth, newHeight int
		var dstImg *image.RGBA

		switch opts.Strategy {
		case "skew":
			newWidth = opts.Width
			newHeight = opts.Height
			dstImg = image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
			draw.CatmullRom.Scale(dstImg, dstImg.Bounds(), img, img.Bounds(), draw.Over, nil)
			img = dstImg

		case "center_aspect_fit":
			newWidth = opts.Width
			newHeight = int(origHeight * (targetWidth / origWidth))
			if float64(opts.Width)/float64(opts.Height) > origWidth/origHeight {
				newHeight = opts.Height
				newWidth = int(origWidth * (targetHeight / origHeight))
			}

			dstImg = image.NewRGBA(image.Rect(0, 0, opts.Width, opts.Height))
			// Fill with black (RGBA is transparent black by default, make it opaque black)
			for i := range dstImg.Pix {
				if i%4 == 3 {
					dstImg.Pix[i] = 255
				} else {
					dstImg.Pix[i] = 0
				}
			}

			offsetX := (opts.Width - newWidth) / 2
			offsetY := (opts.Height - newHeight) / 2
			scaledRect := image.Rect(offsetX, offsetY, offsetX+newWidth, offsetY+newHeight)

			draw.CatmullRom.Scale(dstImg, scaledRect, img, img.Bounds(), draw.Over, nil)
			img = dstImg

		case "center_aspect_cover":
			newHeight = int(origHeight * (targetWidth / origWidth))
			newWidth = opts.Width
			if float64(opts.Height)/float64(opts.Width) > origHeight/origWidth {
				newWidth = int(origWidth * (targetHeight / origHeight))
				newHeight = opts.Height
			}

			dstImg = image.NewRGBA(image.Rect(0, 0, opts.Width, opts.Height))
			
			offsetX := (opts.Width - newWidth) / 2
			offsetY := (opts.Height - newHeight) / 2
			scaledRect := image.Rect(offsetX, offsetY, offsetX+newWidth, offsetY+newHeight)

			draw.CatmullRom.Scale(dstImg, scaledRect, img, img.Bounds(), draw.Over, nil)
			img = dstImg

		case "scale_aspect_cover":
			newWidth = opts.Width
			newHeight = int(origHeight * (targetWidth / origWidth))
			if newHeight < opts.Height {
				newHeight = opts.Height
				newWidth = int(origWidth * (targetHeight / origHeight))
			}
			dstImg = image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
			draw.CatmullRom.Scale(dstImg, dstImg.Bounds(), img, img.Bounds(), draw.Over, nil)
			img = dstImg

		case "scale_aspect_fit":
			fallthrough
		default:
			newWidth = opts.Width
			newHeight = int(origHeight * (targetWidth / origWidth))
			if newHeight > opts.Height {
				newHeight = opts.Height
				newWidth = int(origWidth * (targetHeight / origHeight))
			}
			dstImg = image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
			draw.CatmullRom.Scale(dstImg, dstImg.Bounds(), img, img.Bounds(), draw.Over, nil)
			img = dstImg
		}
	}
	
	var buf bytes.Buffer
	if opts.Format == "jpeg" || opts.Format == "jpg" {
		err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: opts.Quality})
		if err != nil {
			return nil, err
		}
	} else if opts.Format == "png" {
		err := png.Encode(&buf, img)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("unsupported format: %s", opts.Format)
	}

	return buf.Bytes(), nil
}

