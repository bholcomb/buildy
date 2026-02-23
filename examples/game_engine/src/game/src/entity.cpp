// Entity - stub implementation
#include "engine_core.h"
#include "engine_platform.h"
#include "engine_renderer.h"
#include "engine_audio.h"

namespace game {

using EntityId = engine::u32;

class Entity {
public:
    Entity() : m_id(s_nextId++) {}
    EntityId GetId() const { return m_id; }
    void Update(engine::f32) { /* stub */ }
private:
    EntityId m_id;
    static EntityId s_nextId;
};

EntityId Entity::s_nextId = 1;

} // namespace game
