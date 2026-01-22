#pragma once
#include "core/types.h"

namespace engine {
namespace platform {

struct PlatformInfo {
    const char* name;
    u32 processorCount;
    u64 totalMemory;
    u64 availableMemory;
};

Result InitPlatform();
void ShutdownPlatform();
PlatformInfo GetPlatformInfo();

// These are implemented per-platform
Result InitPlatformNative();
void ShutdownPlatformNative();
PlatformInfo GetPlatformInfoNative();

// Misc platform functions
u64 GetPerformanceCounter();
u64 GetPerformanceFrequency();
f64 GetTimeSeconds();
void Sleep(u32 milliseconds);
void* LoadDynamicLibrary(const char* path);
void UnloadDynamicLibrary(void* handle);
void* GetSymbol(void* handle, const char* name);
const char* GetEnvironmentVariable(const char* name);
bool SetEnvironmentVariable(const char* name, const char* value);
const char* GetExecutablePath();
const char* GetHomeDirectory();

} // namespace platform
} // namespace engine
