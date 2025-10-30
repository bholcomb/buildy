// Audio system - depends on core
#include "engine_core.h"

int InitAudio(int sampleRate, int channels) {
    // Initialize using core engine
    InitEngine(sampleRate, channels);
    // Calculate buffer size
    return Add(sampleRate, channels);
}

int ShutdownAudio() {
    return 0;
}
