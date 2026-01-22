#include "platform/platform.h"

#ifdef _WIN32
#define WIN32_LEAN_AND_MEAN
#include <windows.h>

namespace engine {
namespace platform {

static char s_executablePath[MAX_PATH] = {0};

Result InitPlatformNative() {
    GetModuleFileNameA(nullptr, s_executablePath, MAX_PATH);
    return Result::Success;
}

void ShutdownPlatformNative() {
    // Windows cleanup
}

PlatformInfo GetPlatformInfoNative() {
    PlatformInfo info;
    info.name = "Windows";
    info.version = "10+";
    
    SYSTEM_INFO sysInfo;
    GetSystemInfo(&sysInfo);
    info.processorCount = sysInfo.dwNumberOfProcessors;
    
    MEMORYSTATUSEX memInfo;
    memInfo.dwLength = sizeof(memInfo);
    if (GlobalMemoryStatusEx(&memInfo)) {
        info.totalMemory = memInfo.ullTotalPhys;
        info.availableMemory = memInfo.ullAvailPhys;
    } else {
        info.totalMemory = 0;
        info.availableMemory = 0;
    }
    
    return info;
}

u64 GetPerformanceCounter() {
    LARGE_INTEGER counter;
    QueryPerformanceCounter(&counter);
    return counter.QuadPart;
}

u64 GetPerformanceFrequency() {
    LARGE_INTEGER freq;
    QueryPerformanceFrequency(&freq);
    return freq.QuadPart;
}

f64 GetTimeSeconds() {
    LARGE_INTEGER counter, freq;
    QueryPerformanceCounter(&counter);
    QueryPerformanceFrequency(&freq);
    return static_cast<f64>(counter.QuadPart) / static_cast<f64>(freq.QuadPart);
}

void Sleep(u32 milliseconds) {
    ::Sleep(milliseconds);
}

void YieldThread() {
    SwitchToThread();
}

void* LoadDynamicLibrary(const char* path) {
    return LoadLibraryA(path);
}

void UnloadDynamicLibrary(void* handle) {
    if (handle) {
        FreeLibrary(static_cast<HMODULE>(handle));
    }
}

void* GetSymbol(void* handle, const char* name) {
    return reinterpret_cast<void*>(GetProcAddress(static_cast<HMODULE>(handle), name));
}

const char* GetEnvironmentVariable(const char* name) {
    static char buffer[4096];
    DWORD result = GetEnvironmentVariableA(name, buffer, sizeof(buffer));
    return result > 0 ? buffer : nullptr;
}

bool SetEnvironmentVariable(const char* name, const char* value) {
    return SetEnvironmentVariableA(name, value) != 0;
}

void OpenURL(const char* url) {
    ShellExecuteA(nullptr, "open", url, nullptr, nullptr, SW_SHOWNORMAL);
}

const char* GetExecutablePath() {
    return s_executablePath;
}

const char* GetHomeDirectory() {
    static char path[MAX_PATH];
    if (GetEnvironmentVariableA("USERPROFILE", path, MAX_PATH) > 0) {
        return path;
    }
    return "C:\\";
}

} // namespace platform
} // namespace engine

#endif // _WIN32
