// Platform Linux - stub implementation
#include "platform/platform.h"
#include "core/logging.h"

namespace engine {
namespace platform {

Result InitPlatformNative() {
    LOG_INFO(LogCategory::Platform, "Platform: Linux ");
    LOG_INFO(LogCategory::Platform, "Processors: %u, Memory: %llu MB", 
             GetPlatformInfo().processorCount, GetPlatformInfo().totalMemory / (1024*1024));
    return Result::Success;
}

void ShutdownPlatformNative() { /* Linux cleanup stub */ }

PlatformInfo GetPlatformInfoNative() {
    PlatformInfo info = {};
    info.name = "Linux";
    info.processorCount = 4;
    info.totalMemory = 8ULL * 1024 * 1024 * 1024;
    info.availableMemory = 4ULL * 1024 * 1024 * 1024;
    return info;
}

u64 GetPerformanceCounter() { return 0; }
u64 GetPerformanceFrequency() { return 1000000000ULL; }
f64 GetTimeSeconds() { return 0.0; }
void Sleep(u32) { /* stub */ }
void YieldThread() { /* stub */ }
void* LoadDynamicLibrary(const char*) { return nullptr; }
void UnloadDynamicLibrary(void*) { /* stub */ }
void* GetSymbol(void*, const char*) { return nullptr; }
const char* GetEnvironmentVariable(const char*) { return nullptr; }
bool SetEnvironmentVariable(const char*, const char*) { return false; }
void OpenURL(const char*) { /* stub */ }
const char* GetExecutablePath() { return "/usr/bin/game"; }
const char* GetHomeDirectory() { return "/home/user"; }

} // namespace platform
} // namespace engine
