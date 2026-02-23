// Core assert - stub implementation
#include "core/assert.h"
#include <cstdio>
#include <cstdlib>

namespace engine {

void AssertHandler(const char* expr, const char* file, int line, const char* msg) {
    fprintf(stderr, "ASSERT FAILED: %s\n  File: %s:%d\n  Message: %s\n", expr, file, line, msg ? msg : "");
    abort();
}

} // namespace engine
