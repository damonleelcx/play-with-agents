package art

import (
	"hash/fnv"
	"image"
	"image/color"
	"math"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The brand palette (web/src/styles/tokens.css).
var (
	navy950 = rgb(0x06, 0x0a, 0x14)
	navy800 = rgb(0x11, 0x1b, 0x33)
	navy700 = rgb(0x18, 0x25, 0x44)
	ice     = rgb(0xea, 0xf1, 0xff)
	accents = []fcolor{
		rgb(0x3d, 0x7b, 0xff), // electric blue
		rgb(0xff, 0x8a, 0x3d), // ember
		rgb(0x3f, 0xd2, 0xff), // cyan
		rgb(0xb0, 0x7c, 0xff), // violet
		rgb(0x3d, 0xdc, 0x97), // jade
		rgb(0xff, 0x5d, 0x73), // rose
		rgb(0xff, 0xc0, 0x4d), // gold
	}
)

type fcolor struct{ r, g, b float64 }

func rgb(r, g, b uint8) fcolor { return fcolor{float64(r), float64(g), float64(b)} }

func (c fcolor) mix(o fcolor, t float64) fcolor {
	t = clamp01(t)
	return fcolor{c.r + (o.r-c.r)*t, c.g + (o.g-c.g)*t, c.b + (o.b-c.b)*t}
}

func (c fcolor) scale(k float64) fcolor { return fcolor{c.r * k, c.g * k, c.b * k} }

func (c fcolor) rgba() color.RGBA {
	return color.RGBA{u8(c.r), u8(c.g), u8(c.b), 255}
}

func u8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

func clamp01(t float64) float64 { return math.Max(0, math.Min(1, t)) }

// smooth is a 0..1 edge ramp: 1 inside (d < -w/2), 0 outside (d > w/2).
func smooth(d, w float64) float64 { return clamp01(0.5 - d/w) }

// rng is splitmix64: tiny, deterministic, good enough for layout.
type rng struct{ s uint64 }

func (r *rng) next() uint64 {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
func (r *rng) float() float64 { return float64(r.next()>>11) / (1 << 53) }
func (r *rng) intn(n int) int { return int(r.next() % uint64(n)) }

// Procedural draws the deterministic fallback cover for a game: a brand
// gradient keyed by the id's hash, a board-and-pieces motif and, when the
// embedded font has every glyph, the game's name. Same id and name, same
// bytes.
func Procedural(id, name string) ([]byte, error) {
	return EncodeJPEG(ProceduralImage(id, name))
}

// ProceduralImage is Procedural before encoding.
func ProceduralImage(id, name string) *image.RGBA {
	h := fnv.New64a()
	h.Write([]byte(id))
	r := &rng{s: h.Sum64()}
	a1 := accents[r.intn(len(accents))]
	a2 := accents[1] // ember
	if a1 == a2 {
		a2 = accents[0]
	}
	if r.intn(3) == 0 {
		a1, a2 = a2, a1
	}
	W, H := float64(Width), float64(Height)
	img := image.NewRGBA(image.Rect(0, 0, Width, Height))

	// Background: a diagonal navy gradient with two coloured glows.
	g1x, g1y := W*(0.15+0.2*r.float()), H*(0.15+0.25*r.float())
	g2x, g2y := W*(0.75+0.2*r.float()), H*(0.7+0.25*r.float())
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			fx, fy := float64(x), float64(y)
			t := (fx/W*0.6 + fy/H*0.4)
			c := navy700.mix(navy950, t)
			d1 := math.Hypot(fx-g1x, fy-g1y) / (W * 0.55)
			c = c.mix(a1, 0.42*math.Exp(-d1*d1*2.2))
			d2 := math.Hypot(fx-g2x, fy-g2y) / (W * 0.45)
			c = c.mix(a2, 0.32*math.Exp(-d2*d2*2.6))
			// Vignette.
			vx, vy := fx/W-0.5, fy/H-0.5
			c = c.scale(1 - 0.55*(vx*vx+vy*vy))
			img.SetRGBA(x, y, c.rgba())
		}
	}

	drawBoard(img, r, a1, a2)
	drawEmbers(img, r, a2)
	drawName(img, name)
	return img
}

// drawBoard paints a tilted n×n board with a few glowing pieces on the
// right of the cover.
func drawBoard(img *image.RGBA, r *rng, a1, a2 fcolor) {
	W, H := float64(Width), float64(Height)
	n := 3 + r.intn(6) // 3..8
	size := H * (0.56 + 0.1*r.float())
	cx, cy := W*0.70, H*0.46
	ang := (r.float()*2 - 1) * 0.22
	cos, sin := math.Cos(ang), math.Sin(ang)
	cell := size / float64(n)
	// Pieces: about a third of the cells, keyed by the hash.
	pieces := make([]int, n*n) // 0 none, 1 a1, 2 a2, 3 ice
	for i := range pieces {
		if r.float() < 0.34 {
			pieces[i] = 1 + r.intn(3)
		}
	}
	shape := r.intn(2) // 0 discs, 1 rounded squares
	half := size/2 + cell*0.6
	ext := half * 1.5
	x0, x1 := int(math.Max(0, cx-ext)), int(math.Min(W, cx+ext))
	y0, y1 := int(math.Max(0, cy-ext)), int(math.Min(H, cy+ext))
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			u := dx*cos + dy*sin + size/2 // board space, 0..size
			v := -dx*sin + dy*cos + size/2
			base := fromRGBA(img.RGBAAt(x, y))
			c := base
			// Soft drop shadow under the board.
			sd := math.Max(math.Abs(u-size/2-cell*0.15), math.Abs(v-size/2-cell*0.3)) - size/2
			c = c.mix(navy950, 0.55*smooth(sd, cell*0.9))
			// The board face.
			bd := math.Max(math.Abs(u-size/2), math.Abs(v-size/2)) - size/2
			inside := smooth(bd, 1.5)
			if inside > 0 {
				face := navy800.mix(a1, 0.10+0.08*(v/size))
				c = c.mix(face, inside*0.94)
				// Grid lines.
				gu := math.Abs(math.Mod(u+cell*10, cell) - cell/2)
				gv := math.Abs(math.Mod(v+cell*10, cell) - cell/2)
				line := math.Max(smooth(cell/2-gu-1.2, 1.5), smooth(cell/2-gv-1.2, 1.5))
				c = c.mix(a1.mix(ice, 0.25), inside*line*0.55)
				// Pieces.
				i, j := int(u/cell), int(v/cell)
				if i >= 0 && j >= 0 && i < n && j < n && pieces[j*n+i] > 0 {
					pc := []fcolor{a1, a2, ice}[pieces[j*n+i]-1]
					pu, pv := u-(float64(i)+0.5)*cell, v-(float64(j)+0.5)*cell
					rad := cell * 0.34
					var d float64
					if shape == 0 {
						d = math.Hypot(pu, pv) - rad
					} else {
						q := math.Max(math.Abs(pu), math.Abs(pv)) - rad*0.8
						d = q - rad*0.2
					}
					// Glow, body, highlight.
					c = c.mix(pc, 0.35*math.Exp(-math.Max(d, 0)/(cell*0.12))*inside)
					body := smooth(d, 1.5)
					shade := pc.mix(ice, clamp01(0.45-(pu+pv)/(rad*3)))
					c = c.mix(shade.scale(0.85+0.15*clamp01(1-(pu+pv)/(rad*2))), body)
				}
			}
			// The board's rim light.
			rim := math.Abs(bd)
			c = c.mix(a1.mix(ice, 0.3), 0.7*smooth(rim-1.2, 1.8))
			if c != base {
				img.SetRGBA(x, y, c.rgba())
			}
		}
	}
}

// drawEmbers scatters a few small glowing sparks.
func drawEmbers(img *image.RGBA, r *rng, col fcolor) {
	for k := 0; k < 26; k++ {
		px, py := r.float()*float64(Width), r.float()*float64(Height)
		rad := 1.2 + r.float()*3.2
		alpha := 0.25 + 0.6*r.float()
		for y := int(py - rad*4); y <= int(py+rad*4); y++ {
			for x := int(px - rad*4); x <= int(px+rad*4); x++ {
				if x < 0 || y < 0 || x >= Width || y >= Height {
					continue
				}
				d := math.Hypot(float64(x)-px, float64(y)-py)
				t := alpha * math.Exp(-(d*d)/(rad*rad*1.6))
				if t < 0.01 {
					continue
				}
				img.SetRGBA(x, y, fromRGBA(img.RGBAAt(x, y)).mix(col.mix(ice, 0.4), t).rgba())
			}
		}
	}
}

func fromRGBA(c color.RGBA) fcolor { return fcolor{float64(c.R), float64(c.G), float64(c.B)} }

var (
	fontOnce sync.Once
	boldFont *opentype.Font
)

func titleFont() *opentype.Font {
	fontOnce.Do(func() { boldFont, _ = opentype.Parse(gobold.TTF) })
	return boldFont
}

// drawName writes the name at the lower left, at most two lines, with a
// shadow. Names the embedded (Latin) font cannot draw are left off: the
// card shows the name next to the cover anyway.
func drawName(img *image.RGBA, name string) {
	name = strings.Join(strings.Fields(name), " ")
	f := titleFont()
	if name == "" || f == nil {
		return
	}
	maxW := float64(Width) * 0.47
	for _, size := range []float64{84, 72, 62, 54, 46} {
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return
		}
		for _, r := range name {
			if _, ok := face.GlyphAdvance(r); !ok {
				face.Close()
				return
			}
		}
		lines := wrap(face, name, maxW)
		if len(lines) <= 2 {
			lh := size * 1.08
			y := float64(Height) - 64 - lh*float64(len(lines)-1)
			for _, ln := range lines {
				drawText(img, face, ln, 64+3, y+4, color.RGBA{0, 0, 0, 150})
				drawText(img, face, ln, 64, y, color.RGBA{0xf4, 0xf7, 0xff, 255})
				y += lh
			}
			face.Close()
			return
		}
		face.Close()
	}
}

func wrap(face font.Face, s string, maxW float64) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		try := strings.TrimSpace(cur + " " + w)
		if cur != "" && measure(face, try) > maxW {
			lines = append(lines, cur)
			cur = w
		} else {
			cur = try
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	for _, ln := range lines {
		if measure(face, ln) > maxW {
			return append(lines, "", "") // a single word too wide: try smaller
		}
	}
	return lines
}

func measure(face font.Face, s string) float64 {
	return float64(font.MeasureString(face, s)) / 64
}

func drawText(img *image.RGBA, face font.Face, s string, x, y float64, c color.RGBA) {
	d := &font.Drawer{Dst: img, Src: image.NewUniform(c), Face: face,
		Dot: fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(y * 64)}}
	d.DrawString(s)
}
