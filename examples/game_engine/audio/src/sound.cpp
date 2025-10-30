// Sound effects
#include "engine_core.h"

int LoadSound(int sizeKB) {
    return AllocateMemory(sizeKB);
}

int PlaySound(int soundId, int volume) {
    return soundId * volume / 100;
}
