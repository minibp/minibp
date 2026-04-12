// main.cpp - C++ application
#include "core.h"
#include <iostream>

int main() {
    Core core("MyApp");
    core.initialize();
    std::cout << "Running " << core.getName() << std::endl;
    return 0;
}
