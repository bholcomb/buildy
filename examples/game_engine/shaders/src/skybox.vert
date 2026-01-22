#version 450

layout(location = 0) in vec3 inPosition;

layout(set = 0, binding = 0) uniform CameraUBO {
    mat4 view;
    mat4 projection;
    mat4 viewProjection;
    vec4 cameraPosition;
} camera;

layout(location = 0) out vec3 fragTexCoord;

void main() {
    fragTexCoord = inPosition;
    
    // Remove translation from view matrix for skybox
    mat4 viewNoTranslation = mat4(mat3(camera.view));
    
    vec4 pos = camera.projection * viewNoTranslation * vec4(inPosition, 1.0);
    
    // Set z = w so depth is always 1.0 (far plane)
    gl_Position = pos.xyww;
}
