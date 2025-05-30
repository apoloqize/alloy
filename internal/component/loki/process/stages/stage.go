package stages

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/go-kit/log"
	"github.com/grafana/alloy/internal/component/common/loki"
	"github.com/grafana/alloy/internal/featuregate"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	"gopkg.in/yaml.v2"
)

// TODO(@tpaschalis) Let's use this as the list of stages we need to port over.
const (
	StageTypeCRI        = "cri"
	StageTypeDecolorize = "decolorize"
	StageTypeDocker     = "docker"
	StageTypeDrop       = "drop"
	//TODO(thampiotr): Add support for eventlogmessage stage
	StageTypeEventLogMessage    = "eventlogmessage"
	StageTypeGeoIP              = "geoip"
	StageTypeJSON               = "json"
	StageTypeLabel              = "labels"
	StageTypeLabelAllow         = "labelallow"
	StageTypeLabelDrop          = "labeldrop"
	StageTypeLimit              = "limit"
	StageTypeLogfmt             = "logfmt"
	StageTypeLuhn               = "luhn"
	StageTypeMatch              = "match"
	StageTypeMetric             = "metrics"
	StageTypeMultiline          = "multiline"
	StageTypeOutput             = "output"
	StageTypePack               = "pack"
	StageTypePipeline           = "pipeline"
	StageTypeRegex              = "regex"
	StageTypeReplace            = "replace"
	StageTypeSampling           = "sampling"
	StageTypeStaticLabels       = "static_labels"
	StageTypeStructuredMetadata = "structured_metadata"
	StageTypeTemplate           = "template"
	StageTypeTenant             = "tenant"
	StageTypeTimestamp          = "timestamp"
	StageTypeWindowsEvent       = "windowsevent"
)

// Add stages that are not GA. Stages that are not specified here are considered GA.
var stagesUnstable = map[string]featuregate.Stability{
	StageTypeWindowsEvent: featuregate.StabilityExperimental,
}

// Processor takes an existing set of labels, timestamp and log entry and returns either a possibly mutated
// timestamp and log entry
type Processor interface {
	Process(labels model.LabelSet, extracted map[string]interface{}, time *time.Time, entry *string)
	Name() string
}

type Entry struct {
	Extracted map[string]interface{}
	loki.Entry
}

// Stage can receive entries via an inbound channel and forward mutated entries to an outbound channel.
type Stage interface {
	Name() string
	Run(chan Entry) chan Entry
	Cleanup()
}

func (entry *Entry) copy() *Entry {
	out, err := yaml.Marshal(entry)
	if err != nil {
		return nil
	}

	var n *Entry
	err = yaml.Unmarshal(out, &n)
	if err != nil {
		return nil
	}

	return n
}

// stageProcessor Allow to transform a Processor (old synchronous pipeline stage) into an async Stage
type stageProcessor struct {
	Processor

	inspector *inspector
}

func (s stageProcessor) Run(in chan Entry) chan Entry {
	return RunWith(in, func(e Entry) Entry {
		var before *Entry

		if Inspect {
			before = e.copy()
		}

		s.Process(e.Labels, e.Extracted, &e.Timestamp, &e.Line)

		if Inspect {
			s.inspector.inspect(s.Processor.Name(), before, e)
		}

		return e
	})
}

func toStage(p Processor) Stage {
	return &stageProcessor{
		Processor: p,
		inspector: newInspector(os.Stderr, runtime.GOOS == "windows"),
	}
}

func checkFeatureStability(stageName string, minStability featuregate.Stability) error {
	blockStability, exist := stagesUnstable[stageName]
	if exist {
		return featuregate.CheckAllowed(blockStability, minStability, fmt.Sprintf("stage %q", stageName))
	}
	return nil
}

// New creates a new stage for the given type and configuration.
func New(logger log.Logger, jobName *string, cfg StageConfig, registerer prometheus.Registerer, minStability featuregate.Stability) (Stage, error) {
	var (
		s   Stage
		err error
	)
	switch {
	case cfg.DockerConfig != nil:
		s, err = NewDocker(logger, registerer, minStability)
		if err != nil {
			return nil, err
		}
	case cfg.CRIConfig != nil:
		s, err = NewCRI(logger, *cfg.CRIConfig, registerer, minStability)
		if err != nil {
			return nil, err
		}
	case cfg.JSONConfig != nil:
		s, err = newJSONStage(logger, *cfg.JSONConfig)
		if err != nil {
			return nil, err
		}
	case cfg.LogfmtConfig != nil:
		s, err = newLogfmtStage(logger, *cfg.LogfmtConfig)
		if err != nil {
			return nil, err
		}
	case cfg.LuhnFilterConfig != nil:
		s, err = newLuhnFilterStage(*cfg.LuhnFilterConfig)
		if err != nil {
			return nil, err
		}
	case cfg.MetricsConfig != nil:
		s, err = newMetricStage(logger, *cfg.MetricsConfig, registerer)
		if err != nil {
			return nil, err
		}
	case cfg.LabelsConfig != nil:
		s, err = newLabelStage(logger, *cfg.LabelsConfig)
		if err != nil {
			return nil, err
		}
	case cfg.StructuredMetadata != nil:
		s, err = newStructuredMetadataStage(logger, *cfg.StructuredMetadata)
		if err != nil {
			return nil, err
		}
	case cfg.RegexConfig != nil:
		s, err = newRegexStage(logger, *cfg.RegexConfig)
		if err != nil {
			return nil, err
		}
	case cfg.TimestampConfig != nil:
		s, err = newTimestampStage(logger, *cfg.TimestampConfig)
		if err != nil {
			return nil, err
		}
	case cfg.OutputConfig != nil:
		s, err = newOutputStage(logger, *cfg.OutputConfig)
		if err != nil {
			return nil, err
		}
	case cfg.MatchConfig != nil:
		s, err = newMatcherStage(logger, jobName, *cfg.MatchConfig, registerer, minStability)
		if err != nil {
			return nil, err
		}
	case cfg.TemplateConfig != nil:
		s, err = newTemplateStage(logger, *cfg.TemplateConfig)
		if err != nil {
			return nil, err
		}
	case cfg.TenantConfig != nil:
		s, err = newTenantStage(logger, *cfg.TenantConfig)
		if err != nil {
			return nil, err
		}
	case cfg.ReplaceConfig != nil:
		s, err = newReplaceStage(logger, *cfg.ReplaceConfig)
		if err != nil {
			return nil, err
		}
	case cfg.LimitConfig != nil:
		s, err = newLimitStage(logger, *cfg.LimitConfig, registerer)
		if err != nil {
			return nil, err
		}
	case cfg.DropConfig != nil:
		s, err = newDropStage(logger, *cfg.DropConfig, registerer)
		if err != nil {
			return nil, err
		}
	case cfg.MultilineConfig != nil:
		s, err = newMultilineStage(logger, *cfg.MultilineConfig)
		if err != nil {
			return nil, err
		}
	case cfg.PackConfig != nil:
		s = newPackStage(logger, *cfg.PackConfig, registerer)
	case cfg.LabelAllowConfig != nil:
		s, err = newLabelAllowStage(*cfg.LabelAllowConfig)
		if err != nil {
			return nil, err
		}
	case cfg.LabelDropConfig != nil:
		s, err = newLabelDropStage(*cfg.LabelDropConfig)
		if err != nil {
			return nil, err
		}
	case cfg.StaticLabelsConfig != nil:
		s, err = newStaticLabelsStage(logger, *cfg.StaticLabelsConfig)
		if err != nil {
			return nil, err
		}
	case cfg.GeoIPConfig != nil:
		s, err = newGeoIPStage(logger, *cfg.GeoIPConfig)
		if err != nil {
			return nil, err
		}
	case cfg.DecolorizeConfig != nil:
		s, err = newDecolorizeStage(*cfg.DecolorizeConfig)
		if err != nil {
			return nil, err
		}
	case cfg.SamplingConfig != nil:
		s = newSamplingStage(logger, *cfg.SamplingConfig, registerer)
	case cfg.EventLogMessageConfig != nil:
		s = newEventLogMessageStage(logger, cfg.EventLogMessageConfig)
	case cfg.WindowsEventConfig != nil:
		s = newWindowsEventStage(logger, cfg.WindowsEventConfig)
	default:
		panic(fmt.Sprintf("unreachable; should have decoded into one of the StageConfig fields: %+v", cfg))
	}

	if err := checkFeatureStability(s.Name(), minStability); err != nil {
		return nil, err
	}

	return s, nil
}

// Cleanup implements Stage.
func (*stageProcessor) Cleanup() {
	// no-op
}
