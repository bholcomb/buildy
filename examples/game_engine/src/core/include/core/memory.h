#pragma once
#include "core/types.h"

namespace engine {

// Simple allocator interface
class Allocator {
public:
    virtual ~Allocator() = default;
    virtual void* Allocate(usize size) = 0;
    virtual void* Reallocate(void* ptr, usize newSize) = 0;
    virtual void Free(void* ptr) = 0;
};

// Get the default system allocator
Allocator* GetDefaultAllocator();

} // namespace engine
