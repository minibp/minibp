#include "strutil.h"
#include <iostream>
#include <cassert>

int main() {
    // Test to_upper
    assert(strutil::to_upper("hello") == "HELLO");
    
    // Test to_lower
    assert(strutil::to_lower("HELLO") == "hello");
    
    // Test trim
    assert(strutil::trim("  hello  ") == "hello");
    
    // Test join
    std::vector<std::string> parts = {"a", "b", "c"};
    assert(strutil::join(",", parts) == "a,b,c");
    
    std::cout << "All string utility tests passed!" << std::endl;
    return 0;
}
