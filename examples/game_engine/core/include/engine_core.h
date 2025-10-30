#ifndef ENGINE_CORE_H
#define ENGINE_CORE_H

// Engine initialization and shutdown
int InitEngine(int width, int height);
int ShutdownEngine();

// Math utilities
int Add(int a, int b);
int Multiply(int a, int b);
float Distance(float x1, float y1, float x2, float y2);

// Memory management
int AllocateMemory(int size);
int FreeMemory(int address);

#endif // ENGINE_CORE_H

