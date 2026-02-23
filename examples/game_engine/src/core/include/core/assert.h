#pragma once

#include "core/types.h"
#include "core/logging.h"

namespace engine {

void AssertionFailed(const char* expression, const char* file, int line, const char* message = nullptr);
void DebugBreak();

} // namespace engine

// Assert macros
#ifdef NDEBUG
    #define ENGINE_ASSERT(expr) ((void)0)
    #define ENGINE_ASSERT_MSG(expr, msg) ((void)0)
#else
    #define ENGINE_ASSERT(expr) \
        do { \
            if (!(expr)) { \
                engine::AssertionFailed(#expr, __FILE__, __LINE__); \
                engine::DebugBreak(); \
            } \
        } while (0)
    
    #define ENGINE_ASSERT_MSG(expr, msg) \
        do { \
            if (!(expr)) { \
                engine::AssertionFailed(#expr, __FILE__, __LINE__, msg); \
                engine::DebugBreak(); \
            } \
        } while (0)
#endif

// Verify always runs the expression, even in release
#define ENGINE_VERIFY(expr) \
    do { \
        if (!(expr)) { \
            engine::AssertionFailed(#expr, __FILE__, __LINE__); \
        } \
    } while (0)

// Static assert wrapper
#define ENGINE_STATIC_ASSERT(expr) static_assert(expr, #expr)
