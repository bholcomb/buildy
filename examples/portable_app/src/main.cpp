#include <SDL.h>
#include <nlohmann/json.hpp>
#include <iostream>
#include <string>

#ifndef APP_NAME
#define APP_NAME "PortableApp"
#endif

int main(int argc, char* argv[]) {
    (void)argc;
    (void)argv;

    std::cout << "Starting " << APP_NAME << "...\n";

    nlohmann::json config = {
        {"app_name", APP_NAME},
        {"version", "1.0.0"},
        {"window", {
            {"width", 800},
            {"height", 600},
            {"title", APP_NAME}
        }}
    };

    std::cout << "Configuration:\n" << config.dump(2) << "\n";

    if (SDL_Init(SDL_INIT_VIDEO) < 0) {
        std::cerr << "SDL initialization failed: " << SDL_GetError() << "\n";
        return 1;
    }

    SDL_Window* window = SDL_CreateWindow(
        config["window"]["title"].get<std::string>().c_str(),
        SDL_WINDOWPOS_CENTERED,
        SDL_WINDOWPOS_CENTERED,
        config["window"]["width"].get<int>(),
        config["window"]["height"].get<int>(),
        SDL_WINDOW_SHOWN
    );

    if (!window) {
        std::cerr << "Window creation failed: " << SDL_GetError() << "\n";
        SDL_Quit();
        return 1;
    }

    std::cout << "Window created successfully. Running for 2 seconds...\n";

    SDL_Delay(2000);

    SDL_DestroyWindow(window);
    SDL_Quit();

    std::cout << APP_NAME << " finished successfully.\n";
    return 0;
}
