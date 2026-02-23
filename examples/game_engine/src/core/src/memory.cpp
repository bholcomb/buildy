// Core memory - stub implementation
#include "core/memory.h"
#include <cstdlib>

namespace engine {

class DefaultAllocator : public Allocator {
public:
    void* Allocate(usize size) override { return malloc(size); }
    void* Reallocate(void* ptr, usize size) override { return realloc(ptr, size); }
    void Free(void* ptr) override { free(ptr); }
};

static DefaultAllocator s_defaultAllocator;

Allocator* GetDefaultAllocator() { return &s_defaultAllocator; }

} // namespace engine
