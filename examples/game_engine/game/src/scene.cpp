// Scene - stub implementation
#include "engine_core.h"
#include "engine_renderer.h"

namespace game {

class Scene {
public:
    void Load(const char*) { /* stub */ }
    void Unload() { /* stub */ }
    void Update(engine::f32) { /* stub */ }
    void Render(engine::renderer::CommandBuffer*) { /* stub */ }
};

} // namespace game
