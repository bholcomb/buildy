// Music playback
int LoadMusic(int trackId) {
    return trackId + 1000;  // Return music handle
}

int PlayMusic(int musicHandle, int loop) {
    return musicHandle * loop;
}

int StopMusic() {
    return 0;
}
