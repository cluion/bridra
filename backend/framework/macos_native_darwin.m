//go:build darwin && cgo && bridra_macos_native

#import <Foundation/Foundation.h>

int bridra_macos_foundation_available(void) {
  @autoreleasepool {
    return NSClassFromString(@"NSURL") != nil ? 1 : 0;
  }
}
