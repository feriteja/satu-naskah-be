package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	Log   *zap.Logger
	Sugar *zap.SugaredLogger
)

// Init initializes the global logger configuration.
func Init() {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	// Custom JSON config
	encoder := zapcore.NewJSONEncoder(encoderConfig)
	writer := zapcore.AddSync(os.Stdout)

	// Create Core
	core := zapcore.NewCore(encoder, writer, zapcore.InfoLevel)

	Log = zap.New(core, zap.AddCaller())
	Sugar = Log.Sugar()
}
