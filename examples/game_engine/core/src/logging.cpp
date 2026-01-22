// Core logging - stub implementation
#include "core/logging.h"
#include <cstdio>
#include <cstdarg>

namespace engine {

static Logger s_logger;

Logger& Logger::Instance() { return s_logger; }

void Logger::Log(LogLevel level, LogCategory category, const char* file, int line, const char* fmt, ...) {
    (void)file; (void)line;
    const char* levelStr[] = {"TRACE", "DEBUG", "INFO ", "WARN ", "ERROR", "FATAL"};
    const char* catStr[] = {"Core", "Platform", "Renderer", "Audio", "Input", "Game"};
    const char* colors[] = {"\033[90m", "\033[36m", "\033[32m", "\033[33m", "\033[31m", "\033[35m"};
    
    printf("%s[%s] [%s] ", colors[(int)level], levelStr[(int)level], catStr[(int)category]);
    va_list args;
    va_start(args, fmt);
    vprintf(fmt, args);
    va_end(args);
    printf("\033[0m\n");
}

} // namespace engine
