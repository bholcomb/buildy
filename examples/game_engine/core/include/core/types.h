#pragma once

#include <cstdint>
#include <cstddef>

namespace engine {

// Fixed-width integer types
using i8  = int8_t;
using i16 = int16_t;
using i32 = int32_t;
using i64 = int64_t;

using u8  = uint8_t;
using u16 = uint16_t;
using u32 = uint32_t;
using u64 = uint64_t;

using f32 = float;
using f64 = double;

using usize = size_t;
using isize = ptrdiff_t;

// Result type for operations that can fail
enum class Result : i32 {
    Success = 0,
    Error = -1,
    NotFound = -2,
    InvalidArgument = -3,
    OutOfMemory = -4,
    NotInitialized = -5,
    AlreadyInitialized = -6,
};

// Common handle type
struct Handle {
    u32 index;
    u32 generation;
    
    bool IsValid() const { return generation != 0; }
    static Handle Invalid() { return {0, 0}; }
};

// Forward declarations
class Engine;
class Logger;
class MemoryAllocator;

} // namespace engine
