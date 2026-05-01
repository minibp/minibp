#include <stdio.h>

extern void log_message(const char* msg);
extern int is_valid_string(const char* str);
extern int string_length(const char* str);

int main() {
    log_message("Running export test");
    const char* test = "hello world";
    if (is_valid_string(test)) {
        printf("length of '%s' = %d\n", test, string_length(test));
    }
    return 0;
}
