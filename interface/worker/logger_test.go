package worker

import livekitlogger "github.com/livekit/protocol/logger"

type roomIORecordingLogger struct {
	warnMessages  []string
	errorMessages []string
}

func (l *roomIORecordingLogger) Debugw(string, ...any) {}
func (l *roomIORecordingLogger) Infow(string, ...any)  {}
func (l *roomIORecordingLogger) Warnw(msg string, err error, keysAndValues ...any) {
	l.warnMessages = append(l.warnMessages, msg)
}
func (l *roomIORecordingLogger) Errorw(msg string, err error, keysAndValues ...any) {
	l.errorMessages = append(l.errorMessages, msg)
}
func (l *roomIORecordingLogger) WithValues(keysAndValues ...any) livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithUnlikelyValues(keysAndValues ...any) livekitlogger.UnlikelyLogger {
	return livekitlogger.GetDiscardLogger().WithUnlikelyValues(keysAndValues...)
}
func (l *roomIORecordingLogger) WithName(name string) livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithComponent(component string) livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithCallDepth(depth int) livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithItemSampler() livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithoutSampler() livekitlogger.Logger {
	return l
}
func (l *roomIORecordingLogger) WithDeferredValues() (livekitlogger.Logger, livekitlogger.DeferredFieldResolver) {
	return livekitlogger.GetDiscardLogger().WithDeferredValues()
}
