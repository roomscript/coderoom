package participant

import (
	"fmt"
	"math"
)

const (
	participantLightness = 0.78
	participantChroma    = 0.16
	initialHue           = 142.0
	goldenAngle          = 137.50776405003785
)

// colorAllocator generates deterministic, perceptually distributed participant
// colors. Allocated colors are never reused during the registry's lifetime.
type colorAllocator struct {
	nextIndex int
	used      map[string]struct{}
}

func (a *colorAllocator) next() string {
	if a.used == nil {
		a.used = make(map[string]struct{})
	}
	for attempt := 0; ; attempt++ {
		color := participantColor(a.nextIndex, attempt)
		if _, exists := a.used[color]; exists {
			continue
		}
		a.used[color] = struct{}{}
		a.nextIndex++
		return color
	}
}

func participantColor(index, attempt int) string {
	hue := math.Mod(initialHue+float64(index)*goldenAngle+float64(attempt)*goldenAngle/2, 360)
	// Reserve yellow, gold and amber for approval presentation. Distribute
	// participant hues over the remaining 290 degrees without that band.
	// The margin around yellow also covers RGB clipping and rounding.
	hue *= 290.0 / 360.0
	if hue >= 50 {
		hue += 70
	}
	// A narrower hue range needs lightness variation when many participants
	// exhaust the distinct rounded RGB values at the initial lightness.
	lightness := participantLightness + 0.005*float64((attempt/360)%9)
	r, g, b := oklchToSRGB(lightness, participantChroma, hue)
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}

func oklchToSRGB(lightness, chroma, hue float64) (uint8, uint8, uint8) {
	radians := hue * math.Pi / 180
	a := chroma * math.Cos(radians)
	b := chroma * math.Sin(radians)

	l := cube(lightness + 0.3963377774*a + 0.2158037573*b)
	m := cube(lightness - 0.1055613458*a - 0.0638541728*b)
	s := cube(lightness - 0.0894841775*a - 1.291485548*b)

	return linearToSRGB(4.0767416621*l - 3.3077115913*m + 0.2309699292*s),
		linearToSRGB(-1.2684380046*l + 2.6097574011*m - 0.3413193965*s),
		linearToSRGB(-0.0041960863*l - 0.7034186147*m + 1.707614701*s)
}

func cube(value float64) float64 { return value * value * value }

func linearToSRGB(value float64) uint8 {
	value = min(max(value, 0), 1)
	if value <= 0.0031308 {
		value *= 12.92
	} else {
		value = 1.055*math.Pow(value, 1/2.4) - 0.055
	}
	return uint8(math.Round(value * 255))
}
