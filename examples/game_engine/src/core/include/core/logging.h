#pragma once
#include "core/types.h"

namespace engine {

enum class LogLevel : u8 { Trace, Debug, Info, Warn, Error, Fatal };
enum class LogCategory : u8 { Core, Platform, Renderer, Audio, Input, Game };

class Logger {
public:
    static Logger& Instance();
    void Log(LogLevel level, LogCategory category, const char* file, int line, const char* fmt, ...);
};

#define LOG_TRACE(cat, ...) ::engine::Logger::Instance().Log(::engine::LogLevel::Trace, cat, __FILE__, __LINE__, __VA_ARGS__)
#define LOG_DEBUG(cat, ...) ::engine::Logger::Instance().Log(::engine::LogLevel::Debug, cat, __FILE__, __LINE__, __VA_ARGS__)
#define LOG_INFO(cat, ...)  ::engine::Logger::Instance().Log(::engine::LogLevel::Info, cat, __FILE__, __LINE__, __VA_ARGS__)
#define LOG_WARN(cat, ...)  ::engine::Logger::Instance().Log(::engine::LogLevel::Warn, cat, __FILE__, __LINE__, __VA_ARGS__)
#define LOG_ERROR(cat, ...) ::engine::Logger::Instance().Log(::engine::LogLevel::Error, cat, __FILE__, __LINE__, __VA_ARGS__)
#define LOG_FATAL(cat, ...) ::engine::Logger::Instance().Log(::engine::LogLevel::Fatal, cat, __FILE__, __LINE__, __VA_ARGS__)

} // namespace engine
