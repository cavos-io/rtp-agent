package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"strings"
	"time"

	lkmath "github.com/cavos-io/rtp-agent/library/math"
	lkplugin "github.com/cavos-io/rtp-agent/library/plugin"
	"github.com/cavos-io/rtp-agent/library/utils"
	lkimages "github.com/cavos-io/rtp-agent/library/utils/images"
	lklanguage "github.com/cavos-io/rtp-agent/library/utils/language"
)

func runDevModeEnvExact(input json.RawMessage) (any, error) {
	var payload struct {
		EnvValues []string `json:"env_values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.EnvValues == nil {
		payload.EnvValues = []string{"1", "", "true", "on"}
	}

	original, originalPresent := os.LookupEnv("LIVEKIT_DEV_MODE")
	defer func() {
		if originalPresent {
			_ = os.Setenv("LIVEKIT_DEV_MODE", original)
		} else {
			_ = os.Unsetenv("LIVEKIT_DEV_MODE")
		}
	}()

	events := make([]map[string]any, 0, len(payload.EnvValues))
	for _, value := range payload.EnvValues {
		if err := os.Setenv("LIVEKIT_DEV_MODE", value); err != nil {
			return nil, fmt.Errorf("set LIVEKIT_DEV_MODE: %w", err)
		}
		events = append(events, map[string]any{
			"name":   "is_dev_mode",
			"env":    value,
			"result": utils.IsDevMode(),
		})
	}
	return map[string]any{
		"contract": "dev-mode-env-exact",
		"events":   events,
	}, nil
}

func runHostedEnvPresence(input json.RawMessage) (any, error) {
	var payload struct {
		EnvValues []*string `json:"env_values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.EnvValues == nil {
		payload.EnvValues = []*string{nil, ptr(""), ptr("https://hosted.example")}
	}

	original, originalPresent := os.LookupEnv("LIVEKIT_REMOTE_EOT_URL")
	defer func() {
		if originalPresent {
			_ = os.Setenv("LIVEKIT_REMOTE_EOT_URL", original)
		} else {
			_ = os.Unsetenv("LIVEKIT_REMOTE_EOT_URL")
		}
	}()

	events := make([]map[string]any, 0, len(payload.EnvValues))
	for _, value := range payload.EnvValues {
		event := map[string]any{
			"name":   "is_hosted",
			"result": utils.IsHosted(),
		}
		if value == nil {
			if err := os.Unsetenv("LIVEKIT_REMOTE_EOT_URL"); err != nil {
				return nil, err
			}
		} else if err := os.Setenv("LIVEKIT_REMOTE_EOT_URL", *value); err != nil {
			return nil, err
		} else {
			event["env"] = *value
		}
		event["result"] = utils.IsHosted()
		events = append(events, event)
	}
	return map[string]any{"contract": "hosted-env-presence", "events": events}, nil
}

func runCloudURLHostSuffix(input json.RawMessage) (any, error) {
	var payload struct {
		URLValues []string `json:"url_values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.URLValues == nil {
		payload.URLValues = []string{
			"wss://tenant.livekit.cloud",
			"https://tenant.livekit.run/path",
			"http://localhost:7880",
			"://bad-url",
			"https://livekit.cloud.evil.example",
		}
	}

	events := make([]map[string]any, 0, len(payload.URLValues))
	for _, value := range payload.URLValues {
		events = append(events, map[string]any{
			"name":   "is_cloud",
			"url":    value,
			"result": utils.IsCloud(value),
		})
	}
	return map[string]any{"contract": "cloud-url-host-suffix", "events": events}, nil
}

func runCamelToSnakeCase(input json.RawMessage) (any, error) {
	var payload struct {
		NameValues []string `json:"name_values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.NameValues == nil {
		payload.NameValues = []string{"HTTPServerID", "roomID", "JobContext", "already_ok", "URL"}
	}

	events := make([]map[string]any, 0, len(payload.NameValues))
	for _, value := range payload.NameValues {
		events = append(events, map[string]any{
			"name":   "camel_to_snake_case",
			"input":  value,
			"result": utils.CamelToSnakeCase(value),
		})
	}
	return map[string]any{"contract": "camel-to-snake-case", "events": events}, nil
}

func runNodeNameShape(json.RawMessage) (any, error) {
	name := utils.NodeName()
	return map[string]any{
		"contract": "node-name-shape",
		"events": []map[string]any{
			{"name": "node_name", "non_empty": name != ""},
		},
	}, nil
}

func runShortUUIDShape(input json.RawMessage) (any, error) {
	var payload struct {
		Prefixes []string `json:"prefixes"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Prefixes == nil {
		payload.Prefixes = []string{"prefix-"}
	}
	events := make([]map[string]any, 0, len(payload.Prefixes))
	for _, prefix := range payload.Prefixes {
		value := lkmath.ShortUUID(prefix)
		events = append(events, map[string]any{
			"name":       "shortuuid",
			"prefix":     prefix,
			"length":     len(value),
			"has_prefix": len(value) >= len(prefix) && value[:len(prefix)] == prefix,
		})
	}
	return map[string]any{"contract": "shortuuid-shape", "events": events}, nil
}

func runPluginDownloader(json.RawMessage) (any, error) {
	called := false
	downloadErr := fmt.Errorf("download failed")
	lkplugin.RegisterPluginDownloader("title", "version", "package", func() error {
		called = true
		return downloadErr
	})
	registered := lkplugin.RegisteredPlugins()
	var selected lkplugin.Plugin
	for i := len(registered) - 1; i >= 0; i-- {
		if registered[i].Title() == "title" && registered[i].Version() == "version" && registered[i].Package() == "package" {
			selected = registered[i]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("registered plugin not found")
	}
	err := selected.DownloadFiles()
	return map[string]any{
		"contract": "plugin-downloader",
		"events": []map[string]any{
			{
				"name":         "registered_plugin",
				"title":        selected.Title(),
				"version":      selected.Version(),
				"package":      selected.Package(),
				"download_err": err != nil,
				"error_class":  errorClass(err),
				"called":       called,
			},
		},
	}, nil
}

func runLanguageNormalize(input json.RawMessage) (any, error) {
	var payload struct {
		CodeValues []string `json:"code_values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.CodeValues == nil {
		payload.CodeValues = []string{"ZH_hant_tw"}
	}
	events := make([]map[string]any, 0, len(payload.CodeValues))
	for _, value := range payload.CodeValues {
		events = append(events, map[string]any{
			"name":   "normalize_language",
			"input":  value,
			"result": lklanguage.NormalizeLanguage(value),
		})
	}
	return map[string]any{"contract": "language-normalize", "events": events}, nil
}

func runLanguageAccessors(input json.RawMessage) (any, error) {
	var payload struct {
		CodeValues []string `json:"code_values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.CodeValues == nil {
		payload.CodeValues = []string{"cmn-Hans-CN", "multi"}
	}
	events := make([]map[string]any, 0, len(payload.CodeValues))
	for _, value := range payload.CodeValues {
		code := lklanguage.NormalizeLanguage(value)
		events = append(events, map[string]any{
			"name":          "language_accessors",
			"input":         value,
			"normalized":    code,
			"language":      lklanguage.Language(code),
			"iso":           lklanguage.ISO(code),
			"region":        lklanguage.Region(code),
			"language_name": lklanguage.ToLanguageName(code),
		})
	}
	return map[string]any{"contract": "language-accessors", "events": events}, nil
}

func runImageEncodeDefaults(json.RawMessage) (any, error) {
	options := lkimages.NewEncodeOptions()
	return map[string]any{
		"contract": "image-encode-defaults",
		"events": []map[string]any{
			{
				"name":    "encode_options",
				"format":  options.Format,
				"quality": options.Quality,
			},
		},
	}, nil
}

func runImageEncodeFormats(input json.RawMessage) (any, error) {
	var payload struct {
		Formats []string `json:"formats"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	formats := payload.Formats
	if formats == nil {
		formats = []string{"JPEG", "PNG"}
	}
	frame := &lkimages.VideoFrame{
		Width:  1,
		Height: 1,
		Format: "rgba",
		Data:   []byte{255, 0, 0, 255},
	}
	events := make([]map[string]any, 0, len(formats))
	for _, format := range formats {
		encoded, err := lkimages.Encode(frame, lkimages.EncodeOptions{
			Format:  format,
			Quality: 75,
		})
		events = append(events, map[string]any{
			"name":        "encode_format",
			"format":      format,
			"error":       err != nil,
			"error_class": errorClass(err),
			"non_empty":   len(encoded) > 0,
		})
	}
	return map[string]any{"contract": "image-encode-formats", "events": events}, nil
}

func runImageEncodeAlphaOpaque(json.RawMessage) (any, error) {
	frame := &lkimages.VideoFrame{
		Width:  1,
		Height: 1,
		Format: "rgba",
		Data:   []byte{255, 0, 0, 0},
	}
	encoded, err := lkimages.Encode(frame, lkimages.EncodeOptions{Format: "PNG"})
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	r, g, b, a := img.At(0, 0).RGBA()
	return map[string]any{
		"contract": "image-encode-alpha-opaque",
		"events": []map[string]any{
			{
				"name": "decoded_pixel",
				"r":    int(r),
				"g":    int(g),
				"b":    int(b),
				"a":    int(a),
			},
		},
	}, nil
}

func runImageEncodeUnknownResize(json.RawMessage) (any, error) {
	frame := &lkimages.VideoFrame{
		Width:  1,
		Height: 1,
		Format: "rgba",
		Data:   []byte{255, 0, 0, 255},
	}
	_, err := lkimages.Encode(frame, lkimages.EncodeOptions{
		Format:   "PNG",
		Width:    2,
		Height:   2,
		Strategy: "unknown",
	})
	return map[string]any{
		"contract": "image-encode-unknown-resize",
		"events": []map[string]any{
			{
				"name":        "encode",
				"error":       err != nil,
				"error_class": errorClass(err),
			},
		},
	}, nil
}

func runExpFilterInitialMinimum(input json.RawMessage) (any, error) {
	var payload struct {
		Alpha   *float64 `json:"alpha"`
		Exp     *float64 `json:"exp"`
		Initial *float64 `json:"initial"`
		MinVal  *float64 `json:"min_val"`
		Sample  *float64 `json:"sample"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	alpha := floatValue(payload.Alpha, 0.5)
	initial := floatValue(payload.Initial, 10)
	minimum := floatValue(payload.MinVal, 6)
	exp := floatValue(payload.Exp, 1)
	sample := floatValue(payload.Sample, 2)

	filter, err := lkmath.NewExpFilterWithOptions(alpha, lkmath.ExpFilterOptions{
		Initial: &initial,
		MinVal:  &minimum,
	})
	if err != nil {
		return nil, err
	}
	applied := filter.Apply(exp, sample)
	value, ok := filter.Value()
	if !ok {
		return nil, fmt.Errorf("filter value is unset after apply")
	}
	return map[string]any{
		"contract": "exp-filter-initial-minimum",
		"events": []map[string]any{
			{
				"name":   "apply",
				"input":  fmt.Sprintf("alpha=%g,initial=%g,min=%g,exp=%g,sample=%g", alpha, initial, minimum, exp, sample),
				"result": fmt.Sprintf("%g", applied),
			},
			{
				"name":   "value",
				"result": fmt.Sprintf("%g", value),
			},
		},
	}, nil
}

func runExpFilterResetAlpha(input json.RawMessage) (any, error) {
	var payload struct {
		Alpha      *float64 `json:"alpha"`
		Exp        *float64 `json:"exp"`
		First      *float64 `json:"first_sample"`
		ResetAlpha *float64 `json:"reset_alpha"`
		Second     *float64 `json:"second_sample"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	alpha := floatValue(payload.Alpha, 0.5)
	exp := floatValue(payload.Exp, 1)
	first := floatValue(payload.First, 10)
	resetAlpha := floatValue(payload.ResetAlpha, 0.25)
	second := floatValue(payload.Second, 14)

	filter, err := lkmath.NewExpFilterWithOptions(alpha, lkmath.ExpFilterOptions{})
	if err != nil {
		return nil, err
	}
	firstApplied := filter.Apply(exp, first)
	filter.Reset(resetAlpha)
	value, ok := filter.Value()
	secondApplied := filter.Apply(exp, second)
	return map[string]any{
		"contract": "exp-filter-reset-alpha",
		"events": []map[string]any{
			{"name": "first_apply", "result": fmt.Sprintf("%g", firstApplied)},
			{"name": "value_after_reset", "ok": ok, "result": fmt.Sprintf("%g", value)},
			{"name": "second_apply", "result": fmt.Sprintf("%g", secondApplied)},
		},
	}, nil
}

func runExpFilterUpdateBase(input json.RawMessage) (any, error) {
	var payload struct {
		Alpha       *float64 `json:"alpha"`
		Exp         *float64 `json:"exp"`
		Initial     *float64 `json:"initial"`
		Sample      *float64 `json:"sample"`
		UpdatedBase *float64 `json:"updated_base"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	alpha := floatValue(payload.Alpha, 0.5)
	initial := floatValue(payload.Initial, 10)
	updatedBase := floatValue(payload.UpdatedBase, 2)
	exp := floatValue(payload.Exp, 1)
	sample := floatValue(payload.Sample, 14)

	filter, err := lkmath.NewExpFilterWithOptions(alpha, lkmath.ExpFilterOptions{Initial: &initial})
	if err != nil {
		return nil, err
	}
	filter.UpdateBase(updatedBase)
	applied := filter.Apply(exp, sample)
	value, ok := filter.Value()
	return map[string]any{
		"contract": "exp-filter-update-base",
		"events": []map[string]any{
			{"name": "apply", "result": fmt.Sprintf("%g", applied)},
			{"name": "value", "ok": ok, "result": fmt.Sprintf("%g", value)},
		},
	}, nil
}

func runExpFilterInvalidAlpha(input json.RawMessage) (any, error) {
	var payload struct {
		AlphaValues []float64 `json:"alpha_values"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	values := payload.AlphaValues
	if values == nil {
		values = []float64{0, 1.1}
	}

	events := make([]map[string]any, 0, len(values))
	for _, alpha := range values {
		_, err := lkmath.NewExpFilterWithOptions(alpha, lkmath.ExpFilterOptions{})
		events = append(events, map[string]any{
			"name":        "new_filter",
			"alpha":       fmt.Sprintf("%g", alpha),
			"error":       err != nil,
			"error_class": errorClass(err),
		})
	}
	return map[string]any{"contract": "exp-filter-invalid-alpha", "events": events}, nil
}

func runExpFilterMissingSample(input json.RawMessage) (any, error) {
	var payload struct {
		Alpha *float64 `json:"alpha"`
		Exp   *float64 `json:"exp"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	alpha := floatValue(payload.Alpha, 0.5)
	exp := floatValue(payload.Exp, 1)
	filter, err := lkmath.NewExpFilterWithOptions(alpha, lkmath.ExpFilterOptions{})
	if err != nil {
		return nil, err
	}
	_, err = filter.ApplyWithoutSample(exp)
	return map[string]any{
		"contract": "exp-filter-missing-sample",
		"events": []map[string]any{
			{
				"name":        "apply_without_sample",
				"error":       err != nil,
				"error_class": errorClass(err),
			},
		},
	}, nil
}

func runExpFilterLegacyMaxClamp(input json.RawMessage) (any, error) {
	var payload struct {
		Alpha  *float64 `json:"alpha"`
		MaxVal *float64 `json:"max_val"`
		Exp    *float64 `json:"exp"`
		Sample *float64 `json:"sample"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	alpha := floatValue(payload.Alpha, 0.5)
	maximum := floatValue(payload.MaxVal, 5)
	exp := floatValue(payload.Exp, 1)
	sample := floatValue(payload.Sample, 10)

	filter := lkmath.NewExpFilter(alpha, maximum)
	applied := filter.Apply(exp, sample)
	return map[string]any{
		"contract": "exp-filter-legacy-max-clamp",
		"events": []map[string]any{
			{"name": "apply", "result": fmt.Sprintf("%g", applied)},
			{"name": "filtered", "result": fmt.Sprintf("%g", filter.Filtered())},
		},
	}, nil
}

func runMovingAverageWindow(input json.RawMessage) (any, error) {
	var payload struct {
		SampleValues []float64 `json:"sample_values"`
		WindowSize   *int      `json:"window_size"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	windowSize := intValue(payload.WindowSize, 3)
	samples := payload.SampleValues
	if samples == nil {
		samples = []float64{1, 2, 3, 4}
	}

	average := lkmath.NewMovingAverage(windowSize)
	events := []map[string]any{{
		"name": "initial",
		"avg":  fmt.Sprintf("%g", average.GetAvg()),
		"size": average.Size(),
	}}
	for _, sample := range samples {
		average.AddSample(sample)
		events = append(events, map[string]any{
			"name":   "add_sample",
			"sample": fmt.Sprintf("%g", sample),
			"avg":    fmt.Sprintf("%g", average.GetAvg()),
			"size":   average.Size(),
		})
	}
	average.Reset()
	events = append(events, map[string]any{
		"name": "reset",
		"avg":  fmt.Sprintf("%g", average.GetAvg()),
		"size": average.Size(),
	})
	return map[string]any{"contract": "moving-average-window", "events": events}, nil
}

func runBoundedDictPopIfOrder(json.RawMessage) (any, error) {
	dictionary := utils.NewBoundedDict[string, int](4)
	dictionary.Set("oldest", 1)
	dictionary.Set("middle", 2)
	dictionary.Set("newest", 3)

	predicateKey, predicateValue, predicateOK := dictionary.PopIf(func(value int) bool {
		return value%2 == 1
	})
	oldestKey, oldestValue, oldestOK := dictionary.PopIf(nil)

	return map[string]any{
		"contract": "bounded-dict-pop-if-order",
		"events": []map[string]any{
			{
				"name": "predicate_odd",
				"result": map[string]any{
					"key":   predicateKey,
					"value": predicateValue,
					"ok":    predicateOK,
				},
			},
			{
				"name": "pop_oldest",
				"result": map[string]any{
					"key":   oldestKey,
					"value": oldestValue,
					"ok":    oldestOK,
				},
			},
		},
	}, nil
}

type boundedDictValue struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func runBoundedDictEvictOldest(json.RawMessage) (any, error) {
	dictionary := utils.NewBoundedDict[string, int](2)
	dictionary.Set("first", 1)
	dictionary.Set("second", 2)
	_, firstBefore := dictionary.Get("first")
	dictionary.Set("third", 3)
	_, firstAfter := dictionary.Get("first")
	second, secondOK := dictionary.Get("second")
	third, thirdOK := dictionary.Get("third")
	return map[string]any{
		"contract": "bounded-dict-evict-oldest",
		"events": []map[string]any{
			{"name": "first_before_overflow", "ok": firstBefore},
			{"name": "first_after_overflow", "ok": firstAfter},
			{"name": "second_after_overflow", "ok": secondOK, "value": second},
			{"name": "third_after_overflow", "ok": thirdOK, "value": third},
		},
	}, nil
}

func runBoundedDictFactoryOnce(json.RawMessage) (any, error) {
	dictionary := utils.NewBoundedDict[string, boundedDictValue](2)
	factoryCalls := 0
	first := dictionary.SetOrUpdate("key", func() boundedDictValue {
		factoryCalls++
		return boundedDictValue{Name: "new"}
	}, func(value boundedDictValue) boundedDictValue {
		value.Count = 1
		return value
	})
	second := dictionary.SetOrUpdate("key", func() boundedDictValue {
		factoryCalls++
		return boundedDictValue{Name: "unexpected"}
	}, func(value boundedDictValue) boundedDictValue {
		value.Count = 2
		return value
	})
	return map[string]any{
		"contract": "bounded-dict-factory-once",
		"events": []map[string]any{
			{"name": "factory_calls", "result": factoryCalls},
			{"name": "first", "result": first},
			{"name": "second", "result": second},
		},
	}, nil
}

func runBoundedDictInvalidSize(json.RawMessage) (any, error) {
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		_ = utils.NewBoundedDict[string, int](0)
	}()
	return map[string]any{
		"contract": "bounded-dict-invalid-size",
		"events": []map[string]any{
			{
				"name":        "new_bounded_dict",
				"error":       panicked,
				"error_class": boolErrorClass(panicked),
			},
		},
	}, nil
}

func runBoundedDictNilMissing(json.RawMessage) (any, error) {
	dictionary := utils.NewBoundedDict[string, *boundedDictValue](2)
	dictionary.Set("key", nil)
	factoryCalls := 0
	got := dictionary.SetOrUpdate("key", func() *boundedDictValue {
		factoryCalls++
		return &boundedDictValue{Name: "fresh"}
	}, func(value *boundedDictValue) *boundedDictValue {
		value.Count = 1
		return value
	})
	stored, ok := dictionary.Get("key")
	return map[string]any{
		"contract": "bounded-dict-nil-missing",
		"events": []map[string]any{
			{"name": "factory_calls", "result": factoryCalls},
			{"name": "returned", "result": got},
			{"name": "stored", "ok": ok, "result": stored},
		},
	}, nil
}

func runBoundedDictUpdateExisting(json.RawMessage) (any, error) {
	dictionary := utils.NewBoundedDict[string, boundedDictValue](2)
	missing, missingOK := dictionary.UpdateValue("missing", func(value boundedDictValue) boundedDictValue {
		value.Count = 1
		return value
	})
	dictionary.Set("key", boundedDictValue{Name: "existing"})
	got, ok := dictionary.UpdateValue("key", func(value boundedDictValue) boundedDictValue {
		value.Count = 3
		return value
	})
	return map[string]any{
		"contract": "bounded-dict-update-existing",
		"events": []map[string]any{
			{"name": "missing", "ok": missingOK, "result": missing},
			{"name": "existing", "ok": ok, "result": got},
		},
	}, nil
}

func runConnectionPool(input json.RawMessage) (any, error) {
	var payload struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	mode := payload.Mode
	if mode == "" {
		mode = "expired_close_deferred"
	}

	switch mode {
	case "expired_close_deferred":
		next := 0
		closeCalls := 0
		pool := utils.NewConnectionPool(utils.ConnectionPoolOptions[int]{
			MaxSessionDuration: -time.Nanosecond,
			Connect: func(context.Context) (int, error) {
				next++
				return next, nil
			},
			Close: func(context.Context, int) error {
				closeCalls++
				return fmt.Errorf("close failed")
			},
		})
		first, firstErr := pool.Get(context.Background(), time.Second)
		pool.Put(first)
		fresh, freshErr := pool.Get(context.Background(), time.Second)
		return connectionPoolResult(mode, []map[string]any{
			{"name": "first_get", "conn": first, "error": firstErr != nil, "error_class": errorClass(firstErr)},
			{"name": "fresh_get", "conn": fresh, "error": freshErr != nil, "error_class": errorClass(freshErr), "reused": pool.LastConnectionReused},
			{"name": "close_calls", "result": closeCalls},
		}), nil
	case "deferred_close_error_next_get":
		next := 0
		closeCalls := 0
		pool := utils.NewConnectionPool(utils.ConnectionPoolOptions[int]{
			Connect: func(context.Context) (int, error) {
				next++
				return next, nil
			},
			Close: func(context.Context, int) error {
				closeCalls++
				return fmt.Errorf("close failed")
			},
		})
		first, firstErr := pool.Get(context.Background(), time.Second)
		pool.Remove(first)
		fresh, freshErr := pool.Get(context.Background(), time.Second)
		return connectionPoolResult(mode, []map[string]any{
			{"name": "first_get", "conn": first, "error": firstErr != nil, "error_class": errorClass(firstErr)},
			{"name": "fresh_get", "conn": fresh, "error": freshErr != nil, "error_class": errorClass(freshErr), "reused": pool.LastConnectionReused},
			{"name": "close_calls", "result": closeCalls},
		}), nil
	case "invalidate_close_next_get":
		next := 0
		closed := []int{}
		pool := utils.NewConnectionPool(utils.ConnectionPoolOptions[int]{
			Connect: func(context.Context) (int, error) {
				next++
				return next, nil
			},
			Close: func(_ context.Context, conn int) error {
				closed = append(closed, conn)
				return nil
			},
		})
		first, firstErr := pool.Get(context.Background(), time.Second)
		pool.Put(first)
		pool.Invalidate()
		fresh, freshErr := pool.Get(context.Background(), time.Second)
		return connectionPoolResult(mode, []map[string]any{
			{"name": "first_get", "conn": first, "error": firstErr != nil, "error_class": errorClass(firstErr)},
			{"name": "fresh_get", "conn": fresh, "error": freshErr != nil, "error_class": errorClass(freshErr), "reused": pool.LastConnectionReused},
			{"name": "closed", "result": closed},
		}), nil
	case "remove_on_error":
		next := 0
		closed := []int{}
		pool := utils.NewConnectionPool(utils.ConnectionPoolOptions[int]{
			Connect: func(context.Context) (int, error) {
				next++
				return next, nil
			},
			Close: func(_ context.Context, conn int) error {
				closed = append(closed, conn)
				return nil
			},
		})
		withErr := pool.WithConnection(context.Background(), time.Second, func(int) error {
			return context.Canceled
		})
		fresh, freshErr := pool.Get(context.Background(), time.Second)
		return connectionPoolResult(mode, []map[string]any{
			{"name": "with_connection", "error": withErr != nil, "error_class": errorClass(withErr)},
			{"name": "fresh_get", "conn": fresh, "error": freshErr != nil, "error_class": errorClass(freshErr), "reused": pool.LastConnectionReused},
			{"name": "closed", "result": closed},
		}), nil
	case "remove_on_panic":
		next := 0
		closed := []int{}
		pool := utils.NewConnectionPool(utils.ConnectionPoolOptions[int]{
			Connect: func(context.Context) (int, error) {
				next++
				return next, nil
			},
			Close: func(_ context.Context, conn int) error {
				closed = append(closed, conn)
				return nil
			},
		})
		panicked := false
		func() {
			defer func() {
				if recover() != nil {
					panicked = true
				}
			}()
			_ = pool.WithConnection(context.Background(), time.Second, func(int) error {
				panic("boom")
			})
		}()
		fresh, freshErr := pool.Get(context.Background(), time.Second)
		return connectionPoolResult(mode, []map[string]any{
			{"name": "with_connection", "error": panicked, "error_class": boolErrorClass(panicked)},
			{"name": "fresh_get", "conn": fresh, "error": freshErr != nil, "error_class": errorClass(freshErr), "reused": pool.LastConnectionReused},
			{"name": "closed", "result": closed},
		}), nil
	case "close_cancels_prewarm":
		connectStarted := make(chan struct{})
		connectCanceled := make(chan struct{})
		releaseConnect := make(chan struct{})
		pool := utils.NewConnectionPool(utils.ConnectionPoolOptions[int]{
			ConnectTimeout: time.Hour,
			Connect: func(ctx context.Context) (int, error) {
				close(connectStarted)
				select {
				case <-ctx.Done():
					close(connectCanceled)
					return 0, ctx.Err()
				case <-releaseConnect:
					return 1, nil
				}
			},
		})
		pool.Prewarm(context.Background())
		started := waitForChannel(connectStarted, 200*time.Millisecond)
		closeErr := pool.Close(context.Background())
		canceled := waitForChannel(connectCanceled, 200*time.Millisecond)
		if !canceled {
			close(releaseConnect)
		}
		return connectionPoolResult(mode, []map[string]any{
			{"name": "connect_started", "result": started},
			{"name": "close", "error": closeErr != nil, "error_class": errorClass(closeErr)},
			{"name": "connect_canceled", "result": canceled},
		}), nil
	default:
		return nil, fmt.Errorf("unknown connection pool mode %q", mode)
	}
}

func connectionPoolResult(mode string, events []map[string]any) map[string]any {
	return map[string]any{
		"contract": "connection-pool-" + strings.ReplaceAll(mode, "_", "-"),
		"events":   events,
	}
}

func waitForChannel(ch <-chan struct{}, timeout time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	}
}

func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func floatValue(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	return *value
}

func intValue(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func hasAnyKey(values map[string]any, keys ...string) bool {
	for _, key := range keys {
		if _, ok := values[key]; ok {
			return true
		}
	}
	return false
}

func errorClass(err error) string {
	if err == nil {
		return ""
	}
	return "error"
}

func boolErrorClass(ok bool) string {
	if ok {
		return "error"
	}
	return ""
}

func capturePanicMessage(fn func()) (message string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			message = fmt.Sprint(recovered)
		}
	}()
	fn()
	return ""
}

func ptr(value string) *string {
	return &value
}
