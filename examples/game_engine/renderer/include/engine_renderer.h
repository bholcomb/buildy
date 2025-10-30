#ifndef ENGINE_RENDERER_H
#define ENGINE_RENDERER_H

// Renderer initialization and drawing
int InitRenderer(int width, int height);
int DrawFrame();

// Shader system
int CompileShader(int type);
int LinkShaderProgram(int vertexShader, int fragmentShader);

// Texture system
int LoadTexture(int width, int height);
int BindTexture(int textureId);

#endif // ENGINE_RENDERER_H

