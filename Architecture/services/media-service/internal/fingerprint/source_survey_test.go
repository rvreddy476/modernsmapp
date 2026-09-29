package fingerprint

import (
	"fmt"
	"testing"
)

// Survey of lavfi sources for the harness (run with -run Survey -v in the
// worker container): how much informative weight each yields at 2 fps, and
// how a 0.2 s phase shift moves its hashes. A harness reference needs ≥ 10
// informative seconds and a phase sensitivity a real recording would have.
func TestHarnessSurveySources(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	sources := map[string]string{
		"testsrc2":        "testsrc2=size=640x360:rate=30:duration=20",
		"mandelbrot":      "mandelbrot=size=640x360:rate=30",
		"gradients_005":   "gradients=size=640x360:rate=30:speed=0.05:duration=20",
		"gradients_02":    "gradients=size=640x360:rate=30:speed=0.2:duration=20",
		"gradients_1":     "gradients=size=640x360:rate=30:speed=1:duration=20",
		"life":            "life=size=640x360:rate=30:mold=10:ratio=0.3",
		"cellauto":        "cellauto=size=640x360:rate=30",
		"testsrc2_slow":   "testsrc2=size=640x360:rate=30:duration=20,setpts=4*PTS",
		"mandelbrot_slow": "mandelbrot=size=640x360:rate=30,setpts=4*PTS",
	}
	for name, src := range sources {
		ref := lavfi(t, dir, name, src, "", "-t", "20")
		shifted := transcode(t, dir, name+"_shift", ref, "", "23", "-ss", "0.2")
		r := hashFile(t, ref)
		s := hashFile(t, shifted)
		inf := InformativeMs(Weights(r.Frames))
		// Phase: compare shifted frame i (ref time 0.2+0.5i) with ref frame i
		// (time 0.5i) and i+1; take the closer.
		var ds []int
		for i := range s.Frames {
			if i+1 >= len(r.Frames) {
				break
			}
			d := Hamming(s.Frames[i].Hash, r.Frames[i].Hash)
			if d2 := Hamming(s.Frames[i].Hash, r.Frames[i+1].Hash); d2 < d {
				d = d2
			}
			ds = append(ds, d)
		}
		sortInts(ds)
		med := -1
		if len(ds) > 0 {
			med = ds[len(ds)/2]
		}
		t.Log(fmt.Sprintf("%-16s frames %3d informative %5d ms  0.2s-phase nearest-neighbour median Hamming %d", name, len(r.Frames), inf, med))
	}
}
