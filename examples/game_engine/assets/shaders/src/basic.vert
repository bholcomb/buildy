#version 450

// Vertex attributes
layout(location = 0) in vec3 inPosition;
layout(location = 1) in vec3 inNormal;
layout(location = 2) in vec2 inTexCoord;
layout(location = 3) in vec4 inTangent;

// Instance data (optional)
layout(location = 4) in mat4 inModelMatrix;

// Uniform buffer - camera data
layout(set = 0, binding = 0) uniform CameraUBO {
    mat4 view;
    mat4 projection;
    mat4 viewProjection;
    vec4 cameraPosition;
    vec4 cameraDirection;
} camera;

// Push constants - per-object data
layout(push_constant) uniform PushConstants {
    mat4 model;
    mat4 normalMatrix;
} push;

// Output to fragment shader
layout(location = 0) out vec3 fragPosition;
layout(location = 1) out vec3 fragNormal;
layout(location = 2) out vec2 fragTexCoord;
layout(location = 3) out vec3 fragTangent;
layout(location = 4) out vec3 fragBitangent;

void main() {
    // World position
    vec4 worldPos = push.model * vec4(inPosition, 1.0);
    fragPosition = worldPos.xyz;
    
    // Transform normal to world space
    fragNormal = normalize(mat3(push.normalMatrix) * inNormal);
    
    // Pass through texture coordinates
    fragTexCoord = inTexCoord;
    
    // Calculate tangent and bitangent for normal mapping
    fragTangent = normalize(mat3(push.model) * inTangent.xyz);
    fragBitangent = cross(fragNormal, fragTangent) * inTangent.w;
    
    // Final position
    gl_Position = camera.viewProjection * worldPos;
}
