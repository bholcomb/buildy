// Game logic - uses renderer and audio
#include "engine_renderer.h"
#include "engine_audio.h"

int InitGame() {
    // Initialize renderer
    int bufferSize = InitRenderer(1920, 1080);
    
    // Initialize audio
    int audioBuffer = InitAudio(44100, 2);
    
    return bufferSize + audioBuffer;
}

int UpdateGame(int deltaTime) {
    // Draw frame
    DrawFrame();
    
    // Play sound effect
    PlaySound(1, 75);
    
    return deltaTime;
}

int ShutdownGame() {
    return 0;
}
