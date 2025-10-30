// Main entry point
extern int InitGame();
extern int UpdateGame(int deltaTime);
extern int ShutdownGame();

int main() {
    // Initialize game
    InitGame();
    
    // Run game loop (simulate one frame)
    UpdateGame(16);  // 16ms = ~60 FPS
    
    // Shutdown
    ShutdownGame();
    
    return 0;
}
