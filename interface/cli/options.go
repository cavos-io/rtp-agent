package cli

import protologger "github.com/livekit/protocol/logger"

type runOptions struct {
	devMode    bool
	evalRunner EvalRunner
	logger     protologger.Logger
	logLevel   string
	logFormat  string
}

type Option interface {
	apply(options *runOptions)
}

type optionFunc func(options *runOptions)

func (f optionFunc) apply(options *runOptions) { f(options) }

func WithEvalRunner(runner EvalRunner) Option {
	return optionFunc(func(options *runOptions) { options.evalRunner = runner })
}

func WithLogger(logger protologger.Logger) Option {
	return optionFunc(func(options *runOptions) { options.logger = logger })
}
