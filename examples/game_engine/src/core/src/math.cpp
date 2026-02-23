// Core math - stub implementation
#include "core/math.h"

namespace engine {
namespace math {

// Vec2
Vec2 Vec2::operator+(const Vec2& o) const { return {x + o.x, y + o.y}; }
Vec2 Vec2::operator-(const Vec2& o) const { return {x - o.x, y - o.y}; }
Vec2 Vec2::operator*(f32 s) const { return {x * s, y * s}; }
f32 Vec2::Dot(const Vec2& o) const { return x * o.x + y * o.y; }
f32 Vec2::Length() const { return 1.0f; }
Vec2 Vec2::Normalize() const { return *this; }

// Vec3
Vec3 Vec3::operator+(const Vec3& o) const { return {x + o.x, y + o.y, z + o.z}; }
Vec3 Vec3::operator-(const Vec3& o) const { return {x - o.x, y - o.y, z - o.z}; }
Vec3 Vec3::operator*(f32 s) const { return {x * s, y * s, z * s}; }
f32 Vec3::Dot(const Vec3& o) const { return x * o.x + y * o.y + z * o.z; }
Vec3 Vec3::Cross(const Vec3& o) const { return {y * o.z - z * o.y, z * o.x - x * o.z, x * o.y - y * o.x}; }
f32 Vec3::Length() const { return 1.0f; }
Vec3 Vec3::Normalize() const { return *this; }

// Mat4
Mat4::Mat4() { for (int i = 0; i < 16; i++) m[i] = 0; }
Mat4 Mat4::operator*(const Mat4&) const { return Identity(); }
Vec4 Mat4::operator*(const Vec4& v) const { return v; }
Mat4 Mat4::Identity() { Mat4 r; r.m[0] = r.m[5] = r.m[10] = r.m[15] = 1.0f; return r; }
Mat4 Mat4::Translation(const Vec3&) { return Identity(); }
Mat4 Mat4::Scale(const Vec3&) { return Identity(); }
Mat4 Mat4::RotationX(f32) { return Identity(); }
Mat4 Mat4::RotationY(f32) { return Identity(); }
Mat4 Mat4::RotationZ(f32) { return Identity(); }
Mat4 Mat4::Perspective(f32, f32, f32, f32) { return Identity(); }
Mat4 Mat4::Orthographic(f32, f32, f32, f32, f32, f32) { return Identity(); }
Mat4 Mat4::LookAt(const Vec3&, const Vec3&, const Vec3&) { return Identity(); }

// Quat
Quat Quat::FromAxisAngle(const Vec3&, f32) { return Identity(); }
Quat Quat::FromEuler(f32, f32, f32) { return Identity(); }
Quat Quat::operator*(const Quat&) const { return Identity(); }
Vec3 Quat::Rotate(const Vec3& v) const { return v; }
Quat Quat::Normalize() const { return *this; }
Quat Quat::Conjugate() const { return {-x, -y, -z, w}; }
Mat4 Quat::ToMatrix() const { return Mat4::Identity(); }

// Utils
f32 Lerp(f32 a, f32 b, f32 t) { return a + (b - a) * t; }
f32 Clamp(f32 v, f32 min, f32 max) { return v < min ? min : (v > max ? max : v); }
f32 Radians(f32 deg) { return deg * 0.0174532925f; }
f32 Degrees(f32 rad) { return rad * 57.2957795f; }

} // namespace math
} // namespace engine
