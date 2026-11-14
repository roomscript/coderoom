package participant

import (
	"fmt"
	"math"
	"regexp"
	"testing"
)

const (
	darkBackground     = "#111827"
	minimumContrast    = 4.5
	readabilitySamples = 1_000
)

func TestColorAllocator_generatesUniqueHTMLColors(t *testing.T) {
	allocator := colorAllocator{}
	seen := make(map[string]bool)
	valid := regexp.MustCompile(`^#[0-9A-F]{6}$`)

	for range 1_000 {
		color := allocator.next()
		if !valid.MatchString(color) {
			t.Fatalf("expected HTML color, got %q", color)
		}
		if seen[color] {
			t.Fatalf("expected unique color, got %q twice", color)
		}
		seen[color] = true
	}
}

func TestColorAllocator_isDeterministic(t *testing.T) {
	first := colorAllocator{}
	second := colorAllocator{}
	for range 100 {
		firstColor := first.next()
		secondColor := second.next()
		if firstColor != secondColor {
			t.Fatalf("expected equal colors, got %q and %q", firstColor, secondColor)
		}
	}
}

func TestColorAllocator_isReadableOnDarkBackground(t *testing.T) {
	allocator := colorAllocator{}
	for range readabilitySamples {
		color := allocator.next()
		if contrastRatio(color, darkBackground) < minimumContrast {
			t.Fatalf("expected %s to have at least %.1f:1 contrast against %s", color, minimumContrast, darkBackground)
		}
	}
}

func contrastRatio(foreground, background string) float64 {
	foregroundLuminance := relativeLuminance(foreground)
	backgroundLuminance := relativeLuminance(background)
	lighter := max(foregroundLuminance, backgroundLuminance)
	darker := min(foregroundLuminance, backgroundLuminance)
	return (lighter + 0.05) / (darker + 0.05)
}

func relativeLuminance(color string) float64 {
	var red, green, blue uint8
	if _, err := fmt.Sscanf(color, "#%02X%02X%02X", &red, &green, &blue); err != nil {
		panic(err)
	}
	return 0.2126*linearChannel(red) + 0.7152*linearChannel(green) + 0.0722*linearChannel(blue)
}

func linearChannel(channel uint8) float64 {
	value := float64(channel) / 255
	if value <= 0.04045 {
		return value / 12.92
	}
	return math.Pow((value+0.055)/1.055, 2.4)
}

// Check actual rounded/clipped output, rather than repeating the allocator's
// hue formula: this catches yellow introduced by gamut conversion too.
func TestColorAllocatorReservesYellow(t *testing.T) {
	allocator := colorAllocator{}
	for range readabilitySamples {
		color := allocator.next()
		hue := perceptualHue(color)
		if hue >= 55 && hue <= 115 {
			t.Fatalf("participant color %s has reserved yellow/amber hue %.2f", color, hue)
		}
	}
}

func perceptualHue(color string) float64 {
	var red, green, blue uint8
	if _, err := fmt.Sscanf(color, "#%02X%02X%02X", &red, &green, &blue); err != nil {
		panic(err)
	}
	r, g, b := linearChannel(red), linearChannel(green), linearChannel(blue)
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	a := 1.9779984951*l - 2.4285922050*m + 0.4505937099*s
	bb := 0.0259040371*l + 0.7827717662*m - 0.8086757660*s
	return math.Mod(math.Atan2(bb, a)*180/math.Pi+360, 360)
}
