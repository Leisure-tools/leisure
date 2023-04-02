;; dl --- difference lists -*- lexical-binding: t; -*-

;;; Commentary:

;; Difference lists
;; (C) 2015, Bill Burdick
;; ZLIB license
;;
;; They're incredibly simple
;; They append in constant time and
;; They convert to lists in O(n) time
;;
;; What could be better?
;;
;; This code needs that lexical-binding: t at the top; remove it at your own peril

;;; Code:

(require 'cl-lib)

(defun dl (&rest elements)
  "A difference list of ELEMENTS."
  (lambda (y) (if y (append elements y) elements)))
(defun dl/resolve (dl)
  "Resolve difference list DL."
  (funcall dl nil))
(defun dl/append (&rest args)
  "Append difference lists in ARGS."
  (lambda (tail) (dl/sub-append tail args)))
(defun dl/sub-append (tail lists)
  (if (not lists) tail
    (funcall (car lists) (dl/sub-append tail (cdr lists)))))

(provide 'dl)
;;; dl.el ends here
