// Main.java - Main entry point
package com.example;

public class Main {
    public static void main(String[] args) {
        Helper.log("Starting application...");
        System.out.println(Util.greet("World"));
        System.out.println("2 + 3 = " + Util.add(2, 3));
    }
}
