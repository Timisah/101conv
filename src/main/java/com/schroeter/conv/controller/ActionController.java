package com.schroeter.conv.controller;

import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;

import jakarta.servlet.http.HttpServletRequest;

@Controller
public class ActionController {

    @GetMapping("/")
    public String root(HttpServletRequest request) {
        return redirectWithQuery("/html/index.html", request);
    }

    @GetMapping("/index.html")
    public String legacyIndex(HttpServletRequest request) {
        return redirectWithQuery("/html/index.html", request);
    }

    @GetMapping("/config.html")
    public String legacyConfig(HttpServletRequest request) {
        return redirectWithQuery("/html/settings.html", request);
    }

    @GetMapping("/history.html")
    public String legacyHistory(HttpServletRequest request) {
        return redirectWithQuery("/html/history.html", request);
    }

    @GetMapping("/compress.html")
    public String legacyCompress(HttpServletRequest request) {
        return redirectWithQuery("/html/compress.html", request);
    }

    @GetMapping("/settings.html")
    public String legacySettings(HttpServletRequest request) {
        return redirectWithQuery("/html/settings.html", request);
    }

    @GetMapping("/session.html")
    public String legacySession(HttpServletRequest request) {
        return redirectWithQuery("/html/session.html", request);
    }

    @GetMapping("/login.html")
    public String legacyLogin(HttpServletRequest request) {
        return redirectWithQuery("/html/login.html", request);
    }

    @PostMapping("/doAction")
    public String doAction() {
        return "redirect:/";
    }

    private String redirectWithQuery(String target, HttpServletRequest request) {
        String query = request.getQueryString();
        if (query == null || query.isBlank()) {
            return "redirect:" + target;
        }
        return "redirect:" + target + "?" + query;
    }
}
