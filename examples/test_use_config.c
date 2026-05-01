#include <config.h>
#include <stdio.h>

int main() {
#ifdef HAS_PTHREAD
    printf("Pthread support: enabled\n");
#else
    printf("Pthread support: not enabled\n");
#endif
    printf("Version: %s\n", VERSION);
    return 0;
}
