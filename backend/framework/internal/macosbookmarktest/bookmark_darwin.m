//go:build darwin && cgo && bridra_macos_native

#import <Foundation/Foundation.h>

#include <stdlib.h>
#include <string.h>

int bridra_test_create_ephemeral_bookmark(
    const char *path,
    void **bytes,
    size_t *length) {
  @autoreleasepool {
    if (path == NULL || bytes == NULL || length == NULL) {
      return 1;
    }
    *bytes = NULL;
    *length = 0;
    NSString *value = [NSString stringWithUTF8String:path];
    if (value == nil) {
      return 1;
    }
    NSURL *url = [NSURL fileURLWithPath:value];
    NSError *error = nil;
    NSData *bookmark = [url bookmarkDataWithOptions:0
                       includingResourceValuesForKeys:nil
                                        relativeToURL:nil
                                                error:&error];
    if (bookmark == nil || [bookmark length] == 0) {
      return 1;
    }
    void *copy = malloc([bookmark length]);
    if (copy == NULL) {
      return 1;
    }
    memcpy(copy, [bookmark bytes], [bookmark length]);
    *bytes = copy;
    *length = [bookmark length];
    return 0;
  }
}
