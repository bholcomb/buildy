#pragma once
#include "core/types.h"
#include "core/math.h"

namespace engine {
namespace renderer {

class Camera {
public:
    Camera();
    void SetPosition(const math::Vec3& position);
    void SetRotation(const math::Quat& rotation);
    void LookAt(const math::Vec3& target, const math::Vec3& up);
    void SetPerspective(f32 fov, f32 aspect, f32 nearPlane, f32 farPlane);
    void SetOrthographic(f32 left, f32 right, f32 bottom, f32 top, f32 nearPlane, f32 farPlane);
    const math::Vec3& GetPosition() const { return m_position; }
    math::Mat4 GetViewMatrix() const;
    math::Mat4 GetProjectionMatrix() const;
    math::Mat4 GetViewProjectionMatrix() const;
private:
    math::Vec3 m_position;
    math::Quat m_rotation;
    f32 m_fov, m_aspect, m_nearPlane, m_farPlane;
};

} // namespace renderer
} // namespace engine
