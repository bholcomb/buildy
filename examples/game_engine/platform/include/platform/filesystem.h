#pragma once
#include "core/types.h"

namespace engine {
namespace platform {

struct FileHandle {
    void* native;
    bool IsValid() const { return native != nullptr; }
    static FileHandle Invalid() { return {nullptr}; }
};

enum class FileMode : u8 { Read, Write, Append };

FileHandle OpenFile(const char* path, FileMode mode);
void CloseFile(FileHandle file);
usize ReadFile(FileHandle file, void* buffer, usize size);
usize WriteFile(FileHandle file, const void* buffer, usize size);
bool FileExists(const char* path);
bool DirectoryExists(const char* path);
u64 GetFileSize(const char* path);
bool CreateDirectory(const char* path);
const char* GetWorkingDirectory();
const char* GetTempDirectory();

} // namespace platform
} // namespace engine
