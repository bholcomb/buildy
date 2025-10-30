#ifndef ENGINE_AUDIO_H
#define ENGINE_AUDIO_H

// Audio system initialization
int InitAudio(int sampleRate, int channels);
int ShutdownAudio();

// Sound effects
int LoadSound(int sizeKB);
int PlaySound(int soundId, int volume);

// Music playback
int LoadMusic(int trackId);
int PlayMusic(int musicHandle, int loop);
int StopMusic();

#endif // ENGINE_AUDIO_H

