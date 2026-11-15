package loadtest

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
	"gonum.org/v1/plot/vg/vgimg"
)

// reportData is the input shared by the plots and report.md.
type reportData struct {
	samples []Sample
	stages  []StageSummary
	startTS int64
}

// plotFiles are the images written by GeneratePlots.
var plotFiles = []string{"write_rate.png", "response_time.png", "resources.png", "lag.png"}

// queryColors gives each range class a stable colour across both panels.
var queryColors = []color.Color{
	color.RGBA{R: 31, G: 119, B: 180, A: 255},
	color.RGBA{R: 255, G: 127, B: 14, A: 255},
	color.RGBA{R: 44, G: 160, B: 44, A: 255},
	color.RGBA{R: 214, G: 39, B: 40, A: 255},
	color.RGBA{R: 148, G: 103, B: 189, A: 255},
}

// GeneratePlots reads samples.csv and stages.json from dir and writes the
// four report images into the same directory.
func GeneratePlots(dir string) error {
	data, err := loadReportData(dir)
	if err != nil {
		return err
	}

	writeRate, err := writeRatePlot(data)
	if err != nil {
		return err
	}
	if err := savePlots([][]*plot.Plot{{writeRate}}, filepath.Join(dir, plotFiles[0]), 26*vg.Centimeter, 14*vg.Centimeter); err != nil {
		return fmt.Errorf("write %s: %w", plotFiles[0], err)
	}

	response, err := responseTimePlots(data)
	if err != nil {
		return err
	}
	if err := savePlots(response, filepath.Join(dir, plotFiles[1]), 28*vg.Centimeter, 20*vg.Centimeter); err != nil {
		return fmt.Errorf("write %s: %w", plotFiles[1], err)
	}

	resources, height, err := resourcePlots(data)
	if err != nil {
		return err
	}
	if err := savePlots(resources, filepath.Join(dir, plotFiles[2]), 30*vg.Centimeter, height); err != nil {
		return fmt.Errorf("write %s: %w", plotFiles[2], err)
	}

	lag, err := lagPlots(data)
	if err != nil {
		return err
	}
	if err := savePlots(lag, filepath.Join(dir, plotFiles[3]), 26*vg.Centimeter, 18*vg.Centimeter); err != nil {
		return fmt.Errorf("write %s: %w", plotFiles[3], err)
	}
	return nil
}

func loadReportData(dir string) (reportData, error) {
	samples, err := ReadSamples(filepath.Join(dir, samplesFile))
	if err != nil {
		return reportData{}, err
	}
	stages, err := ReadStages(filepath.Join(dir, stagesFile))
	if err != nil {
		return reportData{}, err
	}
	if len(stages) == 0 {
		return reportData{}, errors.New("stages.json has no stages")
	}

	startTS := stages[0].StartedAt
	if startTS == 0 && len(samples) > 0 {
		startTS = samples[0].TS
	}
	return reportData{samples: samples, stages: stages, startTS: startTS}, nil
}

// writeRatePlot draws achieved rows/s and the devices×7 target.
func writeRatePlot(data reportData) (*plot.Plot, error) {
	p := plot.New()
	p.Title.Text = "Write rate"
	p.X.Label.Text = "seconds since start"
	p.Y.Label.Text = "rows/s"

	achieved := samplesToXYs(data, "writer", "rows_per_s")
	maxY := maxXY(achieved)
	if len(achieved) > 0 {
		line, err := plotter.NewLine(achieved)
		if err != nil {
			return nil, fmt.Errorf("write rate line: %w", err)
		}
		line.Width = vg.Points(1.5)
		p.Add(line)
		p.Legend.Add("achieved", line)
	}

	target := targetLine(data)
	if len(target) > 0 {
		line, err := plotter.NewLine(target)
		if err != nil {
			return nil, fmt.Errorf("target line: %w", err)
		}
		line.Width = vg.Points(1.5)
		line.Dashes = []vg.Length{vg.Points(6), vg.Points(4)}
		p.Add(line)
		p.Legend.Add("target (devices x 7)", line)
		maxY = maxFloat64(maxY, maxXY(target))
	}

	maxY = maxFloat64(maxY, 1)
	addStageBoundaries(p, data, 0, maxY*1.15)
	p.Y.Min = 0
	p.Y.Max = maxY * 1.15
	return p, nil
}

// responseTimePlots draws query p50/p95 per range class per stage and the
// scraper/insert p95 in a second panel.
func responseTimePlots(data reportData) ([][]*plot.Plot, error) {
	queryPlot := plot.New()
	queryPlot.Title.Text = "Query response time (solid p95, dashed p50)"
	queryPlot.X.Label.Text = "devices"
	queryPlot.Y.Label.Text = "milliseconds"
	queryPlot.X.Tick.Marker = stageTicks(data.stages)

	for i, rangeName := range queryRanges {
		color := queryColors[i%len(queryColors)]

		p95 := &plotter.Line{
			XYs:       stageValues(data.stages, func(s StageSummary) float64 { return s.QueryP95MS[rangeName] }),
			LineStyle: draw.LineStyle{Color: color, Width: vg.Points(1.5)},
		}
		queryPlot.Add(p95)
		queryPlot.Legend.Add(rangeName+" p95", p95)

		p50 := &plotter.Line{
			XYs: stageValues(data.stages, func(s StageSummary) float64 { return s.QueryP50MS[rangeName] }),
			LineStyle: draw.LineStyle{
				Color:  color,
				Width:  vg.Points(1),
				Dashes: []vg.Length{vg.Points(4), vg.Points(3)},
			},
		}
		queryPlot.Add(p50)
	}

	ioPlot := plot.New()
	ioPlot.Title.Text = "Scrape and insert p95"
	ioPlot.X.Label.Text = "devices"
	ioPlot.Y.Label.Text = "milliseconds"
	ioPlot.X.Tick.Marker = stageTicks(data.stages)

	scrape := &plotter.Line{
		XYs:       stageValues(data.stages, func(s StageSummary) float64 { return s.ScrapeP95MS }),
		LineStyle: draw.LineStyle{Color: queryColors[0], Width: vg.Points(1.5)},
	}
	ioPlot.Add(scrape)
	ioPlot.Legend.Add("scrape_ms p95", scrape)

	insert := &plotter.Line{
		XYs:       stageValues(data.stages, func(s StageSummary) float64 { return s.InsertP95MS }),
		LineStyle: draw.LineStyle{Color: queryColors[1], Width: vg.Points(1.5)},
	}
	ioPlot.Add(insert)
	ioPlot.Legend.Add("insert_ms p95", insert)

	return [][]*plot.Plot{{queryPlot}, {ioPlot}}, nil
}

// resourcePlots draws one small multiple per container with CPU and memory.
func resourcePlots(data reportData) ([][]*plot.Plot, vg.Length, error) {
	containers := containerNames(data.samples)
	if len(containers) == 0 {
		p := plot.New()
		p.Title.Text = "No Docker container statistics available"
		return [][]*plot.Plot{{p}}, 8 * vg.Centimeter, nil
	}

	rows := make([][]*plot.Plot, 0, len(containers))
	for _, container := range containers {
		source := "docker:" + container

		cpu := plot.New()
		cpu.Title.Text = container + " - CPU"
		cpu.X.Label.Text = "seconds since start"
		cpu.Y.Label.Text = "percent"
		cpuPoints := samplesToXYs(data, source, "cpu_percent")
		if len(cpuPoints) > 0 {
			line, err := plotter.NewLine(cpuPoints)
			if err != nil {
				return nil, 0, fmt.Errorf("%s cpu line: %w", container, err)
			}
			line.Width = vg.Points(1.5)
			cpu.Add(line)
			cpu.Legend.Add("CPU %", line)
		}
		cpuMax := maxFloat64(maxXY(cpuPoints), 1) * 1.15
		addStageBoundaries(cpu, data, 0, cpuMax)
		cpu.Y.Min = 0
		cpu.Y.Max = cpuMax

		mem := plot.New()
		mem.Title.Text = container + " - memory"
		mem.X.Label.Text = "seconds since start"
		mem.Y.Label.Text = "MiB"
		memPoints := samplesToXYs(data, source, "mem_mib")
		if len(memPoints) > 0 {
			line, err := plotter.NewLine(memPoints)
			if err != nil {
				return nil, 0, fmt.Errorf("%s memory line: %w", container, err)
			}
			line.Width = vg.Points(1.5)
			mem.Add(line)
			mem.Legend.Add("memory", line)
		}
		memMax := maxFloat64(maxXY(memPoints), 1) * 1.15
		addStageBoundaries(mem, data, 0, memMax)
		mem.Y.Min = 0
		mem.Y.Max = memMax

		rows = append(rows, []*plot.Plot{cpu, mem})
	}

	height := vg.Length(len(rows))*5*vg.Centimeter + 2*vg.Centimeter
	return rows, height, nil
}

// lagPlots draws writer end-to-end lag and consumer backlog.
func lagPlots(data reportData) ([][]*plot.Plot, error) {
	lag := plot.New()
	lag.Title.Text = "End-to-end lag"
	lag.X.Label.Text = "seconds since start"
	lag.Y.Label.Text = "milliseconds"
	lagPoints := samplesToXYs(data, "writer", "e2e_lag_ms_p95")
	if len(lagPoints) > 0 {
		line, err := plotter.NewLine(lagPoints)
		if err != nil {
			return nil, fmt.Errorf("lag line: %w", err)
		}
		line.Width = vg.Points(1.5)
		lag.Add(line)
		lag.Legend.Add("e2e_lag_ms p95", line)
	}
	lagMax := maxFloat64(maxXY(lagPoints), 1) * 1.15
	addStageBoundaries(lag, data, 0, lagMax)
	lag.Y.Min = 0
	lag.Y.Max = lagMax

	pending := plot.New()
	pending.Title.Text = "Consumer pending"
	pending.X.Label.Text = "seconds since start"
	pending.Y.Label.Text = "messages"
	pendingPoints := samplesToXYs(data, "writer", "consumer_pending")
	if len(pendingPoints) > 0 {
		line, err := plotter.NewLine(pendingPoints)
		if err != nil {
			return nil, fmt.Errorf("pending line: %w", err)
		}
		line.Width = vg.Points(1.5)
		pending.Add(line)
		pending.Legend.Add("consumer_pending", line)
	}
	pendingMax := maxFloat64(maxXY(pendingPoints), 1) * 1.15
	addStageBoundaries(pending, data, 0, pendingMax)
	pending.Y.Min = 0
	pending.Y.Max = pendingMax

	return [][]*plot.Plot{{lag}, {pending}}, nil
}

func samplesToXYs(data reportData, source, metric string) plotter.XYs {
	points := make(plotter.XYs, 0, 64)
	for _, sample := range data.samples {
		if sample.Source != source || sample.Metric != metric {
			continue
		}
		points = append(points, plotter.XY{
			X: float64(sample.TS - data.startTS),
			Y: sample.Value,
		})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].X < points[j].X })
	return points
}

func targetLine(data reportData) plotter.XYs {
	points := make(plotter.XYs, 0, len(data.stages)*2)
	for _, stage := range data.stages {
		start := float64(stage.StartedAt - data.startTS)
		end := float64(stage.EndedAt - data.startTS)
		if end <= start {
			end = start + 1
		}
		points = append(points,
			plotter.XY{X: start, Y: stage.TargetRowsPerSecond},
			plotter.XY{X: end, Y: stage.TargetRowsPerSecond},
		)
	}
	return points
}

func stageValues(stages []StageSummary, value func(StageSummary) float64) plotter.XYs {
	points := make(plotter.XYs, 0, len(stages))
	for i, stage := range stages {
		points = append(points, plotter.XY{X: float64(i), Y: value(stage)})
	}
	return points
}

func stageTicks(stages []StageSummary) plot.ConstantTicks {
	ticks := make(plot.ConstantTicks, 0, len(stages))
	for i, stage := range stages {
		ticks = append(ticks, plot.Tick{Value: float64(i), Label: strconv.Itoa(stage.Devices)})
	}
	return ticks
}

// addStageBoundaries marks the start of every stage and the end of the run.
func addStageBoundaries(p *plot.Plot, data reportData, minY, maxY float64) {
	boundaries := make([]float64, 0, len(data.stages)+1)
	for _, stage := range data.stages {
		if stage.StartedAt > 0 {
			boundaries = append(boundaries, float64(stage.StartedAt-data.startTS))
		}
	}
	if last := data.stages[len(data.stages)-1]; last.EndedAt > 0 {
		boundaries = append(boundaries, float64(last.EndedAt-data.startTS))
	}

	seen := make(map[float64]bool, len(boundaries))
	for _, x := range boundaries {
		if seen[x] {
			continue
		}
		seen[x] = true
		line, err := plotter.NewLine(plotter.XYs{{X: x, Y: minY}, {X: x, Y: maxY}})
		if err != nil {
			continue
		}
		line.Color = color.RGBA{R: 120, G: 120, B: 120, A: 255}
		line.Width = vg.Points(0.75)
		line.Dashes = []vg.Length{vg.Points(2), vg.Points(3)}
		p.Add(line)
	}
}

func containerNames(samples []Sample) []string {
	seen := make(map[string]bool)
	names := make([]string, 0)
	for _, sample := range samples {
		if !strings.HasPrefix(sample.Source, "docker:") {
			continue
		}
		name := strings.TrimPrefix(sample.Source, "docker:")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// savePlots lays out a rectangular grid and writes it as a PNG.
func savePlots(plots [][]*plot.Plot, path string, width, height vg.Length) error {
	if len(plots) == 0 || len(plots[0]) == 0 {
		return errors.New("no plots to draw")
	}

	cols := len(plots[0])
	for _, row := range plots {
		if len(row) != cols {
			return errors.New("ragged plot grid")
		}
	}

	canvas := vgimg.NewWith(vgimg.UseWH(width, height), vgimg.UseDPI(96))
	tiles := draw.Tiles{
		Rows: len(plots),
		Cols: cols,
		PadX: 0.4 * vg.Centimeter,
		PadY: 0.4 * vg.Centimeter,
	}
	canvases := plot.Align(plots, tiles, draw.New(canvas))
	for i, row := range plots {
		for j, p := range row {
			p.Draw(canvases[i][j])
		}
	}

	file, err := os.Create(path) //nolint:gosec // report output is not sensitive
	if err != nil {
		return fmt.Errorf("create image: %w", err)
	}
	png := vgimg.PngCanvas{Canvas: canvas}
	_, writeErr := png.WriteTo(file)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func maxXY(points plotter.XYs) float64 {
	maxValue := 0.0
	for _, point := range points {
		maxValue = maxFloat64(maxValue, point.Y)
	}
	return maxValue
}

func maxFloat64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
