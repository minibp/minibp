// core.h - Core library header
#ifndef CORE_H
#define CORE_H

#include <string>

class Core {
public:
    Core(const std::string& name);
    void initialize();
    std::string getName() const;

private:
    std::string name_;
};

#endif
