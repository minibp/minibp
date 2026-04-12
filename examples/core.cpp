// core.cpp - Core library
#include "core.h"
#include <iostream>

Core::Core(const std::string& name) : name_(name) {}

void Core::initialize() {
    std::cout << "Initializing core: " << name_ << std::endl;
}

std::string Core::getName() const {
    return name_;
}
