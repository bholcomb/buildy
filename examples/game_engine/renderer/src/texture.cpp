// Texture system
#include "engine_core.h"

int LoadTexture(int width, int height) {
    // Calculate texture size and allocate memory
    int size = width * height * 4;  // 4 bytes per pixel
    return AllocateMemory(size / 1024);  // Convert to KB
}

int BindTexture(int textureId) {
    return textureId > 0 ? 1 : 0;
}
