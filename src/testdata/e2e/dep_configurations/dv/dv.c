#include "dv.h"

#ifdef BROKEN
#error the build is broken on purpose
#endif

/* 222 in a release build (NDEBUG), 111 in a debug build */
int dv(void) {
#ifdef NDEBUG
    return 222;
#else
    return 111;
#endif
}
