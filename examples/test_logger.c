#include "logger.h"
#include <stdio.h>

int main() {
    // Test basic logging
    log_info("Application started");
    log_warn("This is a warning");
    log_error("This is an error");

    // Test debug level (should not show with default level)
    log_debug("This debug message should not appear");

    // Change log level to show debug
    log_set_level(LOG_LEVEL_DEBUG);
    log_debug("This debug message should appear");

    printf("Logger test completed!\n");
    return 0;
}