#include "logger.h"
#include <stdio.h>
#include <stdarg.h>
#include <time.h>
#include <string.h>
#include <stdlib.h>

static int current_level = LOG_LEVEL_INFO;
static FILE* log_file = NULL;
static int use_colors = 1;

void log_set_level(int level) {
    current_level = level;
}

void log_set_file(FILE* file) {
    log_file = file;
}

void log_enable_colors(int enable) {
    use_colors = enable;
}

static void log_message(int level, const char* level_str, const char* format, va_list args) {
    if (level > current_level)
        return;

    time_t now = time(NULL);
    struct tm* tm_info = localtime(&now);
    char timestamp[20];
    strftime(timestamp, sizeof(timestamp), "%Y-%m-%d %H:%M:%S", tm_info);

    FILE* output = log_file ? log_file : stdout;
    
    if (use_colors && output == stdout) {
        const char* color = "";
        switch (level) {
            case LOG_LEVEL_DEBUG: color = "\033[36m"; break;
            case LOG_LEVEL_INFO:  color = "\033[32m"; break;
            case LOG_LEVEL_WARN:  color = "\033[33m"; break;
            case LOG_LEVEL_ERROR: color = "\033[31m"; break;
            default: color = "\033[0m";
        }
        fprintf(output, "%s[%s]\033[0m [%s%s\033[0m ", timestamp, color, level_str, color);
    } else {
        fprintf(output, "[%s] [%s] ", timestamp, level_str);
    }
    
    va_list args_copy;
    va_copy(args_copy, args);
    
    // Determine the length of the formatted message
    int len = vsnprintf(NULL, 0, format, args_copy);
    va_end(args_copy);

    if (len >= 0) {
        // Allocate buffer and format the message
        char* buffer = malloc(len + 1);
        if (buffer) {
            vsnprintf(buffer, len + 1, format, args);
            fprintf(output, "%s", buffer);
            free(buffer);
        }
    }

    fprintf(output, "\n");
    
    if (log_file) {
        fflush(log_file);
    }
}

void log_info(const char* format, ...) {
    va_list args;
    va_start(args, format);
    log_message(LOG_LEVEL_INFO, "INFO", format, args);
    va_end(args);
}

void log_warn(const char* format, ...) {
    va_list args;
    va_start(args, format);
    log_message(LOG_LEVEL_WARN, "WARN", format, args);
    va_end(args);
}

void log_error(const char* format, ...) {
    va_list args;
    va_start(args, format);
    log_message(LOG_LEVEL_ERROR, "ERROR", format, args);
    va_end(args);
}

void log_debug(const char* format, ...) {
    va_list args;
    va_start(args, format);
    log_message(LOG_LEVEL_DEBUG, "DEBUG", format, args);
    va_end(args);
}

void log_fatal(const char* format, ...) {
    va_list args;
    va_start(args, format);
    log_message(LOG_LEVEL_ERROR, "FATAL", format, args);
    va_end(args);
}

int log_get_level() {
    return current_level;
}

void log_close() {
    if (log_file) {
        fclose(log_file);
        log_file = NULL;
    }
}