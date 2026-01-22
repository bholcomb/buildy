// Filesystem Linux - stub implementation
#include "platform/filesystem.h"

namespace engine {
namespace platform {

FileHandle OpenFile(const char*, FileMode) { return FileHandle::Invalid(); }
void CloseFile(FileHandle) { /* stub */ }
usize ReadFile(FileHandle, void*, usize) { return 0; }
usize WriteFile(FileHandle, const void*, usize) { return 0; }
bool FileExists(const char*) { return false; }
bool DirectoryExists(const char*) { return false; }
u64 GetFileSize(const char*) { return 0; }
bool CreateDirectory(const char*) { return false; }
const char* GetWorkingDirectory() { return "/"; }
const char* GetTempDirectory() { return "/tmp"; }

} // namespace platform
} // namespace engine
