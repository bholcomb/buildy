#include "platform/filesystem.h"
#include "core/memory.h"

#ifdef _WIN32

#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <shlobj.h>
#include <cstdio>
#include <cstring>

namespace engine {
namespace platform {

static char s_workingDirectory[MAX_PATH] = {0};

FileHandle OpenFile(const char* path, FileMode mode) {
    DWORD access = 0;
    DWORD creation = 0;
    
    switch (mode) {
        case FileMode::Read:
            access = GENERIC_READ;
            creation = OPEN_EXISTING;
            break;
        case FileMode::Write:
            access = GENERIC_WRITE;
            creation = CREATE_ALWAYS;
            break;
        case FileMode::Append:
            access = GENERIC_WRITE;
            creation = OPEN_ALWAYS;
            break;
        case FileMode::ReadWrite:
            access = GENERIC_READ | GENERIC_WRITE;
            creation = OPEN_EXISTING;
            break;
    }
    
    HANDLE file = CreateFileA(path, access, FILE_SHARE_READ, nullptr, creation, 
                              FILE_ATTRIBUTE_NORMAL, nullptr);
    
    if (file == INVALID_HANDLE_VALUE) {
        return FileHandle::Invalid();
    }
    
    if (mode == FileMode::Append) {
        SetFilePointer(file, 0, nullptr, FILE_END);
    }
    
    return {file};
}

void CloseFile(FileHandle file) {
    if (file.IsValid()) {
        CloseHandle(static_cast<HANDLE>(file.native));
    }
}

usize ReadFile(FileHandle file, void* buffer, usize size) {
    if (!file.IsValid()) return 0;
    
    DWORD bytesRead = 0;
    ::ReadFile(static_cast<HANDLE>(file.native), buffer, static_cast<DWORD>(size), 
               &bytesRead, nullptr);
    return bytesRead;
}

usize WriteFile(FileHandle file, const void* buffer, usize size) {
    if (!file.IsValid()) return 0;
    
    DWORD bytesWritten = 0;
    ::WriteFile(static_cast<HANDLE>(file.native), buffer, static_cast<DWORD>(size), 
                &bytesWritten, nullptr);
    return bytesWritten;
}

bool SeekFile(FileHandle file, i64 offset, SeekOrigin origin) {
    if (!file.IsValid()) return false;
    
    DWORD moveMethod = FILE_BEGIN;
    switch (origin) {
        case SeekOrigin::Begin:   moveMethod = FILE_BEGIN; break;
        case SeekOrigin::Current: moveMethod = FILE_CURRENT; break;
        case SeekOrigin::End:     moveMethod = FILE_END; break;
    }
    
    LARGE_INTEGER li;
    li.QuadPart = offset;
    return SetFilePointerEx(static_cast<HANDLE>(file.native), li, nullptr, moveMethod) != 0;
}

i64 TellFile(FileHandle file) {
    if (!file.IsValid()) return -1;
    
    LARGE_INTEGER li = {};
    LARGE_INTEGER pos;
    if (SetFilePointerEx(static_cast<HANDLE>(file.native), li, &pos, FILE_CURRENT)) {
        return pos.QuadPart;
    }
    return -1;
}

bool FlushFile(FileHandle file) {
    if (!file.IsValid()) return false;
    return FlushFileBuffers(static_cast<HANDLE>(file.native)) != 0;
}

u8* ReadEntireFile(const char* path, usize* outSize, Allocator* allocator) {
    if (!allocator) allocator = GetDefaultAllocator();
    
    FileHandle file = OpenFile(path, FileMode::Read);
    if (!file.IsValid()) {
        if (outSize) *outSize = 0;
        return nullptr;
    }
    
    LARGE_INTEGER size;
    GetFileSizeEx(static_cast<HANDLE>(file.native), &size);
    
    u8* buffer = static_cast<u8*>(allocator->Allocate(static_cast<usize>(size.QuadPart) + 1));
    if (buffer) {
        ReadFile(file, buffer, static_cast<usize>(size.QuadPart));
        buffer[size.QuadPart] = 0;
    }
    
    CloseFile(file);
    
    if (outSize) *outSize = static_cast<usize>(size.QuadPart);
    return buffer;
}

bool WriteEntireFile(const char* path, const void* data, usize size) {
    FileHandle file = OpenFile(path, FileMode::Write);
    if (!file.IsValid()) return false;
    
    usize written = WriteFile(file, data, size);
    CloseFile(file);
    
    return written == size;
}

bool AppendToFile(const char* path, const void* data, usize size) {
    FileHandle file = OpenFile(path, FileMode::Append);
    if (!file.IsValid()) return false;
    
    usize written = WriteFile(file, data, size);
    CloseFile(file);
    
    return written == size;
}

FileInfo GetFileInfo(const char* path) {
    FileInfo info = {};
    
    WIN32_FILE_ATTRIBUTE_DATA data;
    if (GetFileAttributesExA(path, GetFileExInfoStandard, &data)) {
        info.exists = true;
        info.size = (static_cast<u64>(data.nFileSizeHigh) << 32) | data.nFileSizeLow;
        info.isDirectory = (data.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) != 0;
        info.isReadOnly = (data.dwFileAttributes & FILE_ATTRIBUTE_READONLY) != 0;
    }
    
    return info;
}

bool FileExists(const char* path) {
    DWORD attrs = GetFileAttributesA(path);
    return attrs != INVALID_FILE_ATTRIBUTES && !(attrs & FILE_ATTRIBUTE_DIRECTORY);
}

bool DirectoryExists(const char* path) {
    DWORD attrs = GetFileAttributesA(path);
    return attrs != INVALID_FILE_ATTRIBUTES && (attrs & FILE_ATTRIBUTE_DIRECTORY);
}

u64 GetFileSize(const char* path) {
    WIN32_FILE_ATTRIBUTE_DATA data;
    if (GetFileAttributesExA(path, GetFileExInfoStandard, &data)) {
        return (static_cast<u64>(data.nFileSizeHigh) << 32) | data.nFileSizeLow;
    }
    return 0;
}

u64 GetFileModifiedTime(const char* path) {
    WIN32_FILE_ATTRIBUTE_DATA data;
    if (GetFileAttributesExA(path, GetFileExInfoStandard, &data)) {
        ULARGE_INTEGER time;
        time.LowPart = data.ftLastWriteTime.dwLowDateTime;
        time.HighPart = data.ftLastWriteTime.dwHighDateTime;
        return time.QuadPart;
    }
    return 0;
}

bool CreateDirectory(const char* path) {
    return CreateDirectoryA(path, nullptr) != 0;
}

bool CreateDirectories(const char* path) {
    return SHCreateDirectoryExA(nullptr, path, nullptr) == ERROR_SUCCESS;
}

bool DeleteFile(const char* path) {
    return DeleteFileA(path) != 0;
}

bool DeleteDirectory(const char* path) {
    return RemoveDirectoryA(path) != 0;
}

bool DeleteDirectoryRecursive(const char*) {
    return false;
}

bool CopyFile(const char* src, const char* dst) {
    return CopyFileA(src, dst, FALSE) != 0;
}

bool MoveFile(const char* src, const char* dst) {
    return MoveFileA(src, dst) != 0;
}

bool RenameFile(const char* src, const char* dst) {
    return MoveFileA(src, dst) != 0;
}

void GetAbsolutePath(const char* path, char* outBuffer, usize bufferSize) {
    GetFullPathNameA(path, static_cast<DWORD>(bufferSize), outBuffer, nullptr);
}

void GetDirectoryPath(const char* path, char* outBuffer, usize bufferSize) {
    strncpy(outBuffer, path, bufferSize - 1);
    char* lastSlash = strrchr(outBuffer, '\\');
    if (!lastSlash) lastSlash = strrchr(outBuffer, '/');
    if (lastSlash) *lastSlash = '\0';
}

void GetFileName(const char* path, char* outBuffer, usize bufferSize) {
    const char* lastSlash = strrchr(path, '\\');
    if (!lastSlash) lastSlash = strrchr(path, '/');
    const char* filename = lastSlash ? lastSlash + 1 : path;
    strncpy(outBuffer, filename, bufferSize - 1);
}

void GetFileExtension(const char* path, char* outBuffer, usize bufferSize) {
    const char* dot = strrchr(path, '.');
    if (dot && dot != path) {
        strncpy(outBuffer, dot, bufferSize - 1);
    } else {
        outBuffer[0] = '\0';
    }
}

void JoinPath(const char* base, const char* relative, char* outBuffer, usize bufferSize) {
    snprintf(outBuffer, bufferSize, "%s\\%s", base, relative);
}

void NormalizePath(const char* path, char* outBuffer, usize bufferSize) {
    strncpy(outBuffer, path, bufferSize - 1);
    for (char* p = outBuffer; *p; p++) {
        if (*p == '/') *p = '\\';
    }
}

DirectoryIterator BeginDirectoryIteration(const char* path) {
    DirectoryIterator iter = {};
    
    char searchPath[MAX_PATH];
    snprintf(searchPath, sizeof(searchPath), "%s\\*", path);
    
    WIN32_FIND_DATAA findData;
    HANDLE handle = FindFirstFileA(searchPath, &findData);
    
    if (handle != INVALID_HANDLE_VALUE) {
        iter.native = handle;
        strncpy(iter.current.name, findData.cFileName, sizeof(iter.current.name) - 1);
        iter.current.isDirectory = (findData.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) != 0;
    }
    
    return iter;
}

bool NextDirectoryEntry(DirectoryIterator* iterator) {
    if (!iterator || !iterator->native) return false;
    
    WIN32_FIND_DATAA findData;
    while (FindNextFileA(static_cast<HANDLE>(iterator->native), &findData)) {
        if (strcmp(findData.cFileName, ".") != 0 && strcmp(findData.cFileName, "..") != 0) {
            strncpy(iterator->current.name, findData.cFileName, sizeof(iterator->current.name) - 1);
            iterator->current.isDirectory = (findData.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) != 0;
            return true;
        }
    }
    
    return false;
}

void EndDirectoryIteration(DirectoryIterator* iterator) {
    if (iterator && iterator->native) {
        FindClose(static_cast<HANDLE>(iterator->native));
        iterator->native = nullptr;
    }
}

const char* GetWorkingDirectory() {
    if (s_workingDirectory[0] == '\0') {
        GetCurrentDirectoryA(MAX_PATH, s_workingDirectory);
    }
    return s_workingDirectory;
}

bool SetWorkingDirectory(const char* path) {
    if (SetCurrentDirectoryA(path)) {
        GetCurrentDirectoryA(MAX_PATH, s_workingDirectory);
        return true;
    }
    return false;
}

const char* GetTempDirectory() {
    static char path[MAX_PATH];
    GetTempPathA(MAX_PATH, path);
    return path;
}

const char* GetUserDataDirectory(const char* appName) {
    static char path[MAX_PATH];
    if (SHGetFolderPathA(nullptr, CSIDL_APPDATA, nullptr, 0, path) == S_OK) {
        strncat(path, "\\", sizeof(path) - strlen(path) - 1);
        strncat(path, appName, sizeof(path) - strlen(path) - 1);
    }
    return path;
}

} // namespace platform
} // namespace engine

#endif // _WIN32
