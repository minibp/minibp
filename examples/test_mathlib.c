#include "mathlib.h"
#include <stdio.h>
#include <assert.h>

int main() {
    // Test gcd
    assert(gcd(12, 18) == 6);
    assert(gcd(17, 19) == 1);

    // Test lcm
    assert(lcm(4, 6) == 12);
    assert(lcm(3, 5) == 15);

    // Test is_prime
    assert(is_prime(2) == 1);
    assert(is_prime(17) == 1);
    assert(is_prime(1) == 0);
    assert(is_prime(4) == 0);

    // Test factorial
    assert(factorial(0) == 1);
    assert(factorial(5) == 120);
    assert(factorial(10) == 3628800);

    printf("All math library tests passed!\n");
    return 0;
}