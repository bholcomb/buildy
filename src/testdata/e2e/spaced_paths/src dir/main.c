#include <stdio.h>
#include "calc.h"

const char *greeting(void);

int main(void) {
    printf("%s %d\n", greeting(), add(2, 3));
    return 0;
}
