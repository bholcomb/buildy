#pragma once

#include "core/types.h"

namespace engine {
namespace math {

// 2D Vector
struct Vec2 {
    f32 x, y;
    
    Vec2() : x(0), y(0) {}
    Vec2(f32 x, f32 y) : x(x), y(y) {}
    
    Vec2 operator+(const Vec2& other) const;
    Vec2 operator-(const Vec2& other) const;
    Vec2 operator*(f32 scalar) const;
    f32 Dot(const Vec2& other) const;
    f32 Length() const;
    Vec2 Normalize() const;
};

// 3D Vector
struct Vec3 {
    f32 x, y, z;
    
    Vec3() : x(0), y(0), z(0) {}
    Vec3(f32 x, f32 y, f32 z) : x(x), y(y), z(z) {}
    
    Vec3 operator+(const Vec3& other) const;
    Vec3 operator-(const Vec3& other) const;
    Vec3 operator*(f32 scalar) const;
    f32 Dot(const Vec3& other) const;
    Vec3 Cross(const Vec3& other) const;
    f32 Length() const;
    Vec3 Normalize() const;
};

// 4D Vector
struct Vec4 {
    f32 x, y, z, w;
    
    Vec4() : x(0), y(0), z(0), w(0) {}
    Vec4(f32 x, f32 y, f32 z, f32 w) : x(x), y(y), z(z), w(w) {}
    Vec4(const Vec3& v, f32 w) : x(v.x), y(v.y), z(v.z), w(w) {}
};

// 4x4 Matrix (column-major)
struct Mat4 {
    f32 m[16];
    
    Mat4();
    static Mat4 Identity();
    static Mat4 Translation(const Vec3& t);
    static Mat4 Scale(const Vec3& s);
    static Mat4 RotationX(f32 radians);
    static Mat4 RotationY(f32 radians);
    static Mat4 RotationZ(f32 radians);
    static Mat4 Perspective(f32 fov, f32 aspect, f32 near, f32 far);
    static Mat4 Orthographic(f32 left, f32 right, f32 bottom, f32 top, f32 near, f32 far);
    static Mat4 LookAt(const Vec3& eye, const Vec3& target, const Vec3& up);
    
    Mat4 operator*(const Mat4& other) const;
    Vec4 operator*(const Vec4& v) const;
};

// Quaternion
struct Quat {
    f32 x, y, z, w;
    
    Quat() : x(0), y(0), z(0), w(1) {}
    Quat(f32 x, f32 y, f32 z, f32 w) : x(x), y(y), z(z), w(w) {}
    
    static Quat Identity() { return {0, 0, 0, 1}; }
    static Quat FromAxisAngle(const Vec3& axis, f32 radians);
    static Quat FromEuler(f32 pitch, f32 yaw, f32 roll);
    
    Quat operator*(const Quat& other) const;
    Vec3 Rotate(const Vec3& v) const;
    Quat Normalize() const;
    Quat Conjugate() const;
    Mat4 ToMatrix() const;
};

// Math utilities
f32 Lerp(f32 a, f32 b, f32 t);
f32 Clamp(f32 value, f32 min, f32 max);
f32 Radians(f32 degrees);
f32 Degrees(f32 radians);

} // namespace math
} // namespace engine
