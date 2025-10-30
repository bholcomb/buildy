// Renderer - depends on core
#include "engine_core.h"

int InitRenderer(int width, int height) {
    // Use core engine initialization
    int area = InitEngine(width, height);
    // Calculate buffer size (4 bytes per pixel)
    return Multiply(area, 4);
}

int DrawFrame() {
    return 1;  // Frame drawn successfully
}
