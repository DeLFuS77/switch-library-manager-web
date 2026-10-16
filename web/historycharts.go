package web

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	historyChartWidth  = 600
	historyChartHeight = 140
	// the days the growth of the library and of the disk is worked out from
	historyTrendDays = 90
	// a disk filling later than this is not foreseen
	historyForecastDays = 5 * 365
)

// ChartSeries is a line of a chart, with its color (1 to 6, the colors of the legends).
type ChartSeries struct {
	Label  string
	Color  int
	Points string
	Last   string
	Dashed bool
}

// HistoryGraph is a chart of the history: lines over the same days and scale, with the
// values of each day for the pointer.
type HistoryGraph struct {
	Key    string
	Title  string
	Series []ChartSeries
	From   string
	To     string
	Max    string
	// [{"x": position, "d": date, "v": [value of each series]}]
	Days string
}

// HistoryCharts are the charts of the Statistics page, and what they foretell.
type HistoryCharts struct {
	Graphs []HistoryGraph
	// how much the library grows a month, and when the disk would be full at this rate
	GrowthPerMonth string
	FullOn         string
	FullIn         string
}

type chartLine struct {
	label  string
	color  int
	dashed bool
	value  func(HistoryDay) float64
	format func(float64) string
	// days without the value (older days, before it was recorded) are left out
	known func(HistoryDay) bool
}

// historyCharts draws the history of the library; it needs two days at least.
func historyCharts(days []HistoryDay, lang string) *HistoryCharts {
	if len(days) < 2 {
		return nil
	}
	t := func(text string, args ...any) string { return translatef(lang, text, args...) }
	count := func(value float64) string { return formatNumber(lang, int(value)) }
	size := func(value float64) string { return formatSize(int64(value)) }
	always := func(HistoryDay) bool { return true }
	withDisk := func(day HistoryDay) bool { return day.Disk > 0 }

	charts := &HistoryCharts{}
	add := func(key string, title string, format func(float64) string, lines []chartLine, scale float64) {
		if graph := drawHistory(days, lang, lines, scale); graph != nil {
			graph.Key, graph.Title, graph.Max = key, title, format(graphMax(days, lines, scale))
			charts.Graphs = append(charts.Graphs, *graph)
		}
	}
	add("content", t("Content"), count, []chartLine{
		{label: t("Games"), color: 1, value: func(d HistoryDay) float64 { return float64(d.Games) }, format: count, known: always},
		{label: "DLC", color: 2, value: func(d HistoryDay) float64 { return float64(d.Dlc) }, format: count, known: always},
		{label: t("Updates"), color: 3, value: func(d HistoryDay) float64 { return float64(d.Updates) }, format: count, known: always},
	}, 0)
	diskSize := 0.0
	for _, day := range days {
		diskSize = math.Max(diskSize, float64(day.Disk))
	}
	add("space", t("Space"), size, []chartLine{
		{label: t("Library"), color: 1, value: func(d HistoryDay) float64 { return float64(d.Size) }, format: size, known: always},
		{label: t("Disk used"), color: 4, value: func(d HistoryDay) float64 { return float64(d.Disk - d.Free) }, format: size, known: withDisk},
		{label: t("Disk size"), color: 6, dashed: true, value: func(d HistoryDay) float64 { return float64(d.Disk) }, format: size, known: withDisk},
	}, diskSize)
	saved := false
	for _, day := range days {
		saved = saved || day.Saved > 0
	}
	if saved {
		add("saved", t("Saved"), size, []chartLine{
			{label: t("Saved by compressing"), color: 5, value: func(d HistoryDay) float64 { return float64(d.Saved) }, format: size, known: always},
		}, 0)
	}

	// the trends of the last days
	recent := []HistoryDay{}
	last, _ := time.Parse("2006-01-02", days[len(days)-1].Date)
	for _, day := range days {
		if date, err := time.Parse("2006-01-02", day.Date); err == nil && last.Sub(date) <= historyTrendDays*24*time.Hour {
			recent = append(recent, day)
		}
	}
	if perDay, ok := dailyTrend(recent, func(d HistoryDay) float64 { return float64(d.Size) }, always); ok && perDay > 0 {
		charts.GrowthPerMonth = formatSize(int64(perDay * 30))
	}
	if perDay, ok := dailyTrend(recent, func(d HistoryDay) float64 { return float64(d.Disk - d.Free) }, withDisk); ok && perDay > 0 {
		if latest := days[len(days)-1]; latest.Disk > 0 {
			left := float64(latest.Free) / perDay
			if left < historyForecastDays {
				charts.FullOn = formatDate(lang, last.Add(time.Duration(left*24)*time.Hour))
				switch {
				case left < 14:
					charts.FullIn = t("%v days", formatNumber(lang, int(math.Ceil(left))))
				case left < 60:
					charts.FullIn = t("%v weeks", formatNumber(lang, int(math.Round(left/7))))
				default:
					charts.FullIn = t("%v months", formatNumber(lang, int(math.Round(left/30))))
				}
			}
		}
	}
	return charts
}

// graphMax is the top of the scale of a chart: the scale given, or the highest value.
func graphMax(days []HistoryDay, lines []chartLine, scale float64) float64 {
	for _, day := range days {
		for _, line := range lines {
			if line.known(day) {
				scale = math.Max(scale, line.value(day))
			}
		}
	}
	return scale
}

// drawHistory draws the lines over the days, placed by their dates; it returns nil when no
// line has two days.
func drawHistory(days []HistoryDay, lang string, lines []chartLine, scale float64) *HistoryGraph {
	first, err1 := time.Parse("2006-01-02", days[0].Date)
	last, err2 := time.Parse("2006-01-02", days[len(days)-1].Date)
	if err1 != nil || err2 != nil || !last.After(first) {
		return nil
	}
	top := graphMax(days, lines, scale)
	if top <= 0 {
		top = 1
	}
	span := last.Sub(first).Hours()
	x := func(day HistoryDay) (float64, bool) {
		date, err := time.Parse("2006-01-02", day.Date)
		return date.Sub(first).Hours() * historyChartWidth / span, err == nil
	}
	graph := &HistoryGraph{From: formatDate(lang, first), To: formatDate(lang, last)}
	for _, line := range lines {
		points := []string{}
		lastValue := ""
		for _, day := range days {
			position, ok := x(day)
			if !ok || !line.known(day) {
				continue
			}
			y := historyChartHeight - 4 - line.value(day)*(historyChartHeight-8)/top
			points = append(points, fmt.Sprintf("%.1f,%.1f", position, y))
			lastValue = line.format(line.value(day))
		}
		if len(points) < 2 {
			continue
		}
		graph.Series = append(graph.Series, ChartSeries{Label: line.label, Color: line.color, Points: strings.Join(points, " "), Last: lastValue, Dashed: line.dashed})
	}
	if len(graph.Series) == 0 {
		return nil
	}
	type pointerDay struct {
		X      float64  `json:"x"`
		Date   string   `json:"d"`
		Values []string `json:"v"`
	}
	pointer := []pointerDay{}
	for _, day := range days {
		position, ok := x(day)
		if !ok {
			continue
		}
		entry := pointerDay{X: math.Round(position*10) / 10, Values: []string{}}
		if date, err := time.Parse("2006-01-02", day.Date); err == nil {
			entry.Date = formatDate(lang, date)
		}
		for _, line := range lines {
			value := "–"
			if line.known(day) {
				value = line.format(line.value(day))
			}
			if hasSeries(graph.Series, line.label) {
				entry.Values = append(entry.Values, value)
			}
		}
		pointer = append(pointer, entry)
	}
	data, _ := json.Marshal(pointer)
	graph.Days = string(data)
	return graph
}

func hasSeries(series []ChartSeries, label string) bool {
	for _, s := range series {
		if s.Label == label {
			return true
		}
	}
	return false
}

// dailyTrend is how much a value changes a day, by a least squares line over the days that
// know it; it needs a week of them at least.
func dailyTrend(days []HistoryDay, value func(HistoryDay) float64, known func(HistoryDay) bool) (float64, bool) {
	xs, ys := []float64{}, []float64{}
	var origin time.Time
	for _, day := range days {
		date, err := time.Parse("2006-01-02", day.Date)
		if err != nil || !known(day) {
			continue
		}
		if origin.IsZero() {
			origin = date
		}
		xs = append(xs, date.Sub(origin).Hours()/24)
		ys = append(ys, value(day))
	}
	if len(xs) < 2 || xs[len(xs)-1]-xs[0] < 7 {
		return 0, false
	}
	n := float64(len(xs))
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		sxy += xs[i] * ys[i]
	}
	denominator := n*sxx - sx*sx
	if denominator == 0 {
		return 0, false
	}
	return (n*sxy - sx*sy) / denominator, true
}
