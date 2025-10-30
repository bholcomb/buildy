// Math utilities
int Add(int a, int b) {
    return a + b;
}

int Multiply(int a, int b) {
    return a * b;
}

float Distance(float x1, float y1, float x2, float y2) {
    float dx = x2 - x1;
    float dy = y2 - y1;
    return dx * dx + dy * dy;  // Squared distance (avoiding sqrt)
}
