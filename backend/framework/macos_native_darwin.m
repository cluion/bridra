//go:build darwin && cgo && bridra_macos_native

#import <Foundation/Foundation.h>

#include <stdlib.h>

enum {
  BridraBookmarkResolved = 0,
  BridraBookmarkStale = 1,
  BridraBookmarkResolveFailed = 2,
  BridraBookmarkAccessDenied = 3,
};

int bridra_macos_foundation_available(void) {
  @autoreleasepool {
    return NSClassFromString(@"NSURL") != nil ? 1 : 0;
  }
}

int bridra_macos_resolve_bookmark(
    const void *bytes,
    size_t length,
    int persistent,
    char **path,
    void **handle) {
  @autoreleasepool {
    if (bytes == NULL || length == 0 || path == NULL || handle == NULL) {
      return BridraBookmarkResolveFailed;
    }
    *path = NULL;
    *handle = NULL;

    NSData *bookmark = [NSData dataWithBytes:bytes length:length];
    BOOL stale = NO;
    NSError *error = nil;
    NSURLBookmarkResolutionOptions options = 0;
    if (persistent) {
      options = NSURLBookmarkResolutionWithSecurityScope |
                NSURLBookmarkResolutionWithoutUI;
    }
    NSURL *url = [NSURL URLByResolvingBookmarkData:bookmark
                                          options:options
                                    relativeToURL:nil
                              bookmarkDataIsStale:&stale
                                            error:&error];
    if (url == nil) {
      return BridraBookmarkResolveFailed;
    }
    if (stale) {
      if (!persistent) {
        [url stopAccessingSecurityScopedResource];
      }
      return BridraBookmarkStale;
    }
    if (persistent && ![url startAccessingSecurityScopedResource]) {
      return BridraBookmarkAccessDenied;
    }
    if (![url isFileURL] || [[url path] length] == 0) {
      [url stopAccessingSecurityScopedResource];
      return BridraBookmarkResolveFailed;
    }

    const char *utf8Path = [[url path] fileSystemRepresentation];
    char *copiedPath = strdup(utf8Path);
    if (copiedPath == NULL) {
      [url stopAccessingSecurityScopedResource];
      return BridraBookmarkResolveFailed;
    }
    [url retain];
    *path = copiedPath;
    *handle = url;
    return BridraBookmarkResolved;
  }
}

void bridra_macos_release_resource(void *handle) {
  @autoreleasepool {
    if (handle == NULL) {
      return;
    }
    NSURL *url = (NSURL *)handle;
    [url stopAccessingSecurityScopedResource];
    [url release];
  }
}
