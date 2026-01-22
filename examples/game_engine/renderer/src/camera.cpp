// Camera - stub implementation
#include "renderer/camera.h"

namespace engine {
namespace renderer {

Camera::Camera() : m_position({0,0,0}), m_rotation(), m_fov(60), m_aspect(16.0f/9.0f), m_nearPlane(0.1f), m_farPlane(1000) {}
void Camera::SetPosition(const math::Vec3& pos) { m_position = pos; }
void Camera::SetRotation(const math::Quat& rot) { m_rotation = rot; }
void Camera::LookAt(const math::Vec3&, const math::Vec3&) { /* stub */ }
void Camera::SetPerspective(f32 fov, f32 aspect, f32 near, f32 far) { m_fov = fov; m_aspect = aspect; m_nearPlane = near; m_farPlane = far; }
void Camera::SetOrthographic(f32, f32, f32, f32, f32 near, f32 far) { m_nearPlane = near; m_farPlane = far; }
math::Mat4 Camera::GetViewMatrix() const { return math::Mat4::Identity(); }
math::Mat4 Camera::GetProjectionMatrix() const { return math::Mat4::Identity(); }
math::Mat4 Camera::GetViewProjectionMatrix() const { return math::Mat4::Identity(); }

} // namespace renderer
} // namespace engine
