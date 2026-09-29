package fingerprint

import "sort"

// Border crop (plan 12.2): one rectangle per video, the median of
// cropdetect-style luma bounds over up to CropSampleFrames non-flat frames,
// applied to every frame before it is resized to 64×64. Letterbox and
// pillarbox are removed; a blurred-background fill is not (a separate
// evaluation stratum).

const (
	// CropLumaThreshold: a row or column whose brightest pixel is at or
	// below this is border (24/255).
	CropLumaThreshold = 24
	// CropSampleFrames: how many non-flat frames vote on the rectangle.
	CropSampleFrames = 20
	// CropMaxLookahead bounds the frames buffered while the vote is open,
	// so a video that opens with minutes of black cannot hold them all.
	CropMaxLookahead = 300
	// CropMinKeep: a rectangle narrower than this fraction of the frame on
	// either axis is not a border but the picture; the crop is dropped.
	CropMinKeep = 0.25
)

// Rect is a half-open crop rectangle in source pixels.
type Rect struct{ X0, Y0, X1, Y1 int }

// Full is the rectangle that crops nothing.
func Full(side int) Rect { return Rect{0, 0, side, side} }

// FrameBounds finds the luma bounds of one frame: the first and last rows
// and columns that hold a pixel brighter than CropLumaThreshold. ok is
// false when the whole frame is at or below the threshold.
func FrameBounds(g Grey) (r Rect, ok bool) {
	side := g.Side
	top, bottom, left, right := side, -1, side, -1
	for y := 0; y < side; y++ {
		row := g.Pix[y*side : (y+1)*side]
		for x, p := range row {
			if p > CropLumaThreshold {
				if y < top {
					top = y
				}
				bottom = y
				if x < left {
					left = x
				}
				if x > right {
					right = x
				}
			}
		}
	}
	if bottom < 0 {
		return Full(side), false
	}
	return Rect{X0: left, Y0: top, X1: right + 1, Y1: bottom + 1}, true
}

// CropVote collects per-frame bounds and decides the video's rectangle.
type CropVote struct {
	side   int
	bounds []Rect
	seen   int
}

// NewCropVote starts a vote for frames of the given side.
func NewCropVote(side int) *CropVote { return &CropVote{side: side} }

// Offer adds one frame. Flat frames do not vote. Returns true once the vote
// has enough samples (or has looked ahead as far as it will).
func (c *CropVote) Offer(g Grey, flat bool) (decided bool) {
	c.seen++
	if !flat && len(c.bounds) < CropSampleFrames {
		if r, ok := FrameBounds(g); ok {
			c.bounds = append(c.bounds, r)
		}
	}
	return len(c.bounds) >= CropSampleFrames || c.seen >= CropMaxLookahead
}

// Decide returns the median rectangle, or the full frame when there were no
// votes or the median would keep less than CropMinKeep of either axis.
func (c *CropVote) Decide() Rect {
	if len(c.bounds) == 0 {
		return Full(c.side)
	}
	r := Rect{
		X0: medianInt(c.bounds, func(r Rect) int { return r.X0 }),
		Y0: medianInt(c.bounds, func(r Rect) int { return r.Y0 }),
		X1: medianInt(c.bounds, func(r Rect) int { return r.X1 }),
		Y1: medianInt(c.bounds, func(r Rect) int { return r.Y1 }),
	}
	minKeep := int(float64(c.side) * CropMinKeep)
	if r.X1-r.X0 < minKeep || r.Y1-r.Y0 < minKeep || r.X1 <= r.X0 || r.Y1 <= r.Y0 {
		return Full(c.side)
	}
	return r
}

func medianInt(rs []Rect, f func(Rect) int) int {
	v := make([]int, len(rs))
	for i, r := range rs {
		v[i] = f(r)
	}
	sort.Ints(v)
	return v[len(v)/2]
}
