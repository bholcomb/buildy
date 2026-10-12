#include <stdio.h>
int main(void) {
#ifdef NDEBUG
    const char *assertions = "ndebug";
#else
    const char *assertions = "asserts";
#endif
#ifdef __OPTIMIZE__
    const char *optimized = "optimized";
#else
    const char *optimized = "unoptimized";
#endif
    printf("%s %s\n", assertions, optimized);
    return 0;
}
