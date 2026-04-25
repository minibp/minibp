#include "arrayutil.h"
#include <stdio.h>
#include <assert.h>
#include <string.h>

int main() {
    int arr[] = {3, 1, 4, 1, 5, 9, 2, 6};
    size_t size = sizeof(arr) / sizeof(arr[0]);

    // Test array_sum
    assert(array_sum(arr, size) == 31);

    // Test array_average
    assert(array_average(arr, size) == 3.875);

    // Test array_max
    assert(array_max(arr, size) == 9);

    // Test array_min
    assert(array_min(arr, size) == 1);

    // Test array_reverse
    int arr2[] = {1, 2, 3, 4, 5};
    array_reverse(arr2, 5);
    assert(array_max(arr2, 5) == 5);
    assert(array_min(arr2, 5) == 1);

    printf("All array utility tests passed!\n");
    return 0;
}