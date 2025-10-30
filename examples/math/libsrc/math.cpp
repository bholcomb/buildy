
#include "mymath.h"
#include <iostream>

int add(int a, int b) {
    #ifdef DEBUG
    std::cout << "Debug: Adding " << a << " + " << b << std::endl;
    #endif
    return a + b;
}

int multiply(int a, int b) {
    return a * b;
}
