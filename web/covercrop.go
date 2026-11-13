package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/internal/durable"
)

// The cover chosen in Edit, and the image a NUP takes as its icon with Set the icon, can be
// cropped first, as in NXPress: the window shows a square that stays where it is, and the image
// is moved and zoomed under it; Fit whole takes the whole image, on white around it. The page
// tells the square in parts of the image, not in its pixels, so the server cuts the same part of
// the copy it has, whatever its size.

// CoverCrop is the part of an image chosen: a square at X, Y of side Size, X and Size told in
// parts of its width and Y in parts of its height. With Fit the whole image is taken, on white.
type CoverCrop struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Size float64 `json:"size"`
	Fit  bool    `json:"fit"`
}

const (
	// a crop that cannot be one (sent by hand)
	errCoverCrop = errorString("The part of the image chosen cannot be cut.")
	// the cover to crop is not there (the game has none yet)
	errCoverCropNoCover = errorString("The game has no cover yet to crop: choose an image first.")
)

// coverCropFrom reads the crop a form sends (cropX, cropY and cropSize, or cropFit); nil when none
// was chosen.
func coverCropFrom(r *http.Request) (*CoverCrop, error) {
	if r.FormValue("cropFit") == "true" {
		return &CoverCrop{Fit: true}, nil
	}
	if r.FormValue("cropSize") == "" {
		return nil, nil
	}
	number := func(name string) (float64, bool) {
		value, err := strconv.ParseFloat(r.FormValue(name), 64)
		return value, err == nil && value >= 0 && value <= 1
	}
	x, okX := number("cropX")
	y, okY := number("cropY")
	size, okSize := number("cropSize")
	if !okX || !okY || !okSize || size <= 0 {
		return nil, errCoverCrop
	}
	return &CoverCrop{X: x, Y: y, Size: size}, nil
}

// square is the part of a picture of those bounds the crop takes, kept inside it.
func (c CoverCrop) square(bounds image.Rectangle) image.Rectangle {
	width, height := bounds.Dx(), bounds.Dy()
	side := min(width, height)
	size := min(max(1, int(c.Size*float64(width)+0.5)), side)
	x := min(max(int(c.X*float64(width)+0.5), 0), width-size)
	y := min(max(int(c.Y*float64(height)+0.5), 0), height-size)
	return image.Rect(0, 0, size, size).Add(bounds.Min.Add(image.Pt(x, y)))
}

// cut is the part of a picture the crop chose: a square of it, or the whole picture in the middle
// of a white square with Fit.
func (c CoverCrop) cut(source image.Image) image.Image {
	bounds := source.Bounds()
	if c.Fit {
		side := max(bounds.Dx(), bounds.Dy())
		result := image.NewNRGBA(image.Rect(0, 0, side, side))
		draw.Draw(result, result.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		at := image.Pt((side-bounds.Dx())/2, (side-bounds.Dy())/2)
		draw.Draw(result, image.Rectangle{Min: at, Max: at.Add(bounds.Size())}, source, bounds.Min, draw.Over)
		return result
	}
	part := c.square(bounds)
	result := image.NewNRGBA(image.Rect(0, 0, part.Dx(), part.Dy()))
	draw.Draw(result, result.Bounds(), source, part.Min, draw.Src)
	return result
}

// cropImageData cuts an image file as the crop chose, as a PNG.
func cropImageData(data []byte, crop CoverCrop) ([]byte, error) {
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errOverrideNotImage
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, crop.cut(source)); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

// coverToCrop is the cover Edit crops when no new image was chosen: the one chosen before (named
// chosen), else the one of the titles database.
func (web *Web) coverToCrop(id string, chosen string) ([]byte, error) {
	if chosen != "" {
		if data, err := os.ReadFile(web.overrideCoverPath(chosen)); err == nil {
			return data, nil
		}
	}
	prefix := strings.ToLower(id[:min(len(id), 13)])
	if data := web.databaseCover(prefix, maxOverrideCoverBytes); data != nil {
		return data, nil
	}
	return nil, errCoverCropNoCover
}

// databaseCover is the cover of titledb of the game of a title ID prefix: the copy the app keeps,
// else downloaded; nil when there is none.
func (web *Web) databaseCover(prefix string, limit int64) []byte {
	switchDB, localDB := web.state.get()
	if localDB != nil {
		if game, ok := localDB.TitlesMap[prefix]; ok && game.Icon != "" {
			if path, ok := web.imagePath(game.Icon); ok {
				if info, err := os.Stat(path); err == nil && info.Size() <= limit {
					if data, err := os.ReadFile(path); err == nil {
						return data
					}
				}
			}
		}
	}
	if switchDB != nil {
		if title, ok := switchDB.TitlesMap[prefix]; ok && title.Attributes.IconUrl != "" {
			if data, err := db.DownloadImage(title.Attributes.IconUrl, limit); err == nil {
				return data
			}
		}
	}
	return nil
}

// nupIconCrops keeps the crop chosen for the icon of each NUP until its task gives it, by the path
// of the NUP: a task waiting in the queue, or cut by a restart, still has it.
type nupIconCrops struct {
	path string
}

const NUP_ICON_CROPS_FILE = "nup-icon-crops.json"

func (web *Web) nupIconCrops() *nupIconCrops {
	return &nupIconCrops{path: filepath.Join(web.dataFolder, NUP_ICON_CROPS_FILE)}
}

var nupIconCropsMutex sync.Mutex

func (c *nupIconCrops) read() map[string]CoverCrop {
	crops := map[string]CoverCrop{}
	if data, err := os.ReadFile(c.path); err == nil {
		json.Unmarshal(data, &crops)
	}
	return crops
}

// set keeps the crop of the NUP at path; nil forgets it.
func (c *nupIconCrops) set(path string, crop *CoverCrop) error {
	nupIconCropsMutex.Lock()
	defer nupIconCropsMutex.Unlock()
	crops := c.read()
	if crop == nil {
		if _, ok := crops[path]; !ok {
			return nil
		}
		delete(crops, path)
	} else {
		crops[path] = *crop
	}
	if len(crops) == 0 {
		if err := os.Remove(c.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(crops)
	if err != nil {
		return err
	}
	return durable.WriteFile(c.path, data)
}

// get is the crop kept for the NUP at path.
func (c *nupIconCrops) get(path string) (CoverCrop, bool) {
	nupIconCropsMutex.Lock()
	defer nupIconCropsMutex.Unlock()
	crop, ok := c.read()[path]
	return crop, ok
}
