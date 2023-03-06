;; -*- lexical-binding: true;  -*-
;;; leisure-mode.el --- server for connections to Leisure

;; Copyright (c) 2023 Bill Burdick and TEAM CTHULHU
;; Keywords: orgmode, leisure
;; Version: 0.1
;; Package-Requires: ((websocket "0.0") (cl-lib "0.0"))
;;
;; The Leisure project is licensed with the MIT License:
;;
;; Copyright (c) 2023 Bill Burdick and TEAM CTHULHU
;;
;; Permission is hereby granted, free of charge, to any person obtaining a copy
;; of this software and associated documentation files (the "Software"), to deal
;; in the Software without restriction, including without limitation the rights
;; to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
;; copies of the Software, and to permit persons to whom the Software is
;; furnished to do so, subject to the following conditions:
;;
;; The above copyright notice and this permission notice shall be included in all
;; copies or substantial portions of the Software.
;;
;; THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
;; IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
;; FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
;; AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
;; LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
;; OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
;; SOFTWARE.
;;
;;; Commentary:

;; This is a client that allows Emacs to collaborate on Leisure documents.

(require 'dl)
(require 'cl-lib)
(require 'f)

(defgroup leisure ()
  "Customization for leisure.")

(defvar leisure-info nil)

(defvar leisure-reverting-buffers nil)

(defcustom leisure-program (f-join user-emacs-directory "leisure")
  "Location of the leisure program."
  :type 'string
  :group 'leisure)

(defcustom leisure-peer "emacs"
  "Location of the leisure program."
  :type 'string
  :group 'leisure)

(defcustom leisure-min-wait 0.25
  "Minimum time to wait after activity before sending an edit to Leisure"
  :type 'number
  :group 'leisure)

(defcustom leisure-max-wait 0.75
  "Maximum time to wait after activity before sending an edit to Leisure"
  :type 'number
  :group 'leisure)

(defcustom leisure-verbosity 0
  "-1: messages
0: error messages only
1: high-level diagnostics
2: low-level diagnostics"
  :type 'integer
  :group 'leisure)

(define-minor-mode leisure-mode
  "Used when connected to a browser (☢)."
  :lighter "☢"
  ;;:init-value nil
  ;;:keymap nil
  :group 'leisure
  (leisure-toggle-mode))

(cl-defstruct leisure-data
  "information about the leisure connection"
  cookies
  session
  document-alias
  com-buffer-name
  changes
  wait-start
  activity-timer
  flush-timer
  monitoring)

(defun leisure-validate-program ()
  (message "validating leisure")
  (if (f-exists? leisure-program)
      (if (f-executable? leisure-program)
          t
        (leisure-diag 0 "Leisure program '%s' is not executable" leisure-program))
    (leisure-diag 0 "No leisure program '%s'" leisure-program)))

(defun leisure-toggle-mode ()
  (message "Toggle leisure mode to %s, leisure-info: %s" leisure-mode leisure-info)
  (if (not leisure-mode)
      (if leisure-info
          (leisure-disconnect))
    (if (not leisure-info)
        (progn
          ;; Disable mode temporarily until the browser connects
          ;; Connecting will enable the mode again
          (setq leisure-mode nil)
          (if (leisure-validate-program)
              (leisure-start))))))

(defun leisure-compute-info ()
  (let ((filename (buffer-file-name)))
    (make-leisure-data
     :monitoring t
     :cookies (format "%s.%s.leisure" filename leisure-peer)
     :session (format "%s-%d-%s" leisure-peer (emacs-pid) filename)
     :document-alias filename
     :com-buffer-name (format "*leisure-connection:%s*" filename)
     :changes (dl))))

(defun leisure-update-info ()
  (let* ((old leisure-info)
         (new (leisure-compute-info)))
    (if (and (not (equal (leisure-data-document-alias old) (leisure-data-document-alias new)))
             (not (f-exists? (leisure-data-cookies new))))
        (progn
          (f-copy (leisure-data-cookies old) (leisure-data-cookies new))
          (if (f-exists? (leisure-data-cookies new))
              (progn
                (setq leisure-info new)
                (f-delete (leisure-data-cookies old))))))))

(defun leisure-start ()
  (message "Starting leisure...")
  (make-local-variable 'leisure-info)
  (setq leisure-info (leisure-compute-info))
  (leisure-diag 1 "Connecting...")
  (let* ((cookies (leisure-data-cookies leisure-info))
         (connect (leisure-call-program-filter
                   (buffer-string)                      ; input
                   'leisure-parse                       ; filter
                   "session" "connect"
                   "-cookies" (leisure-cookies)
                   "-lock"
                   "-doc" (leisure-full-path)           ; alias
                   (format "%s-%d" leisure-peer (emacs-pid)) ; peer name
                   )))
    (leisure-diag 1 "Connected")
    (if (eql 0 (car connect))
        (leisure-add-change-hooks)
      (setq leisure-mode t)))
  (leisure-update))

(defun leisure-disconnect ()
  (if leisure-info
      (progn
        (leisure-call-program nil "session" "unlock" "-cookies" (leisure-cookies))
        (setq leisure-info nil)
        (kill-buffer "leisure-update")
        (kill-buffer "leisure-update-errors"))))

(defun leisure-should-monitor ()
  "Return whether any change should be monitored right now."
  (and leisure-info (leisure-data-monitoring leisure-info)))

(defun leisure-monitor-changes (status)
  (setf (leisure-data-monitoring leisure-info) status))

;; buffer change handling
(defun leisure-after-change (start end oldLength)
  "Called on change (START, END, OLDLENGTH) to leisure buffers."
  (if (leisure-should-monitor)
      (let ((newText (buffer-substring-no-properties start end)))
        (cl-decf start)
        (leisure-change start oldLength newText))
    ;(leisure-print "Not monitoring after change")
    ))

(defun leisure-buf-string ()
  "Get the buffer string without properties."
  (buffer-substring-no-properties 1 (+ 1  (buffer-size))))

(defun leisure-before-revert ()
  "Preserve leisure info for after revert."
  (if (leisure-should-monitor)
      (leisure-save-revert-info)))

(defun leisure-peek-revert-info ()
  "Get a buffer's revert info."
  (cdr (assq (intern (buffer-name)) leisure-reverting-buffers)))

(defun leisure-save-revert-info ()
  "Set a buffer's revert info."
  (let ((name (intern (buffer-name))))
    (setq leisure-reverting-buffers
          (cons (cons name leisure-info)
                (assq-delete-all name leisure-reverting-buffers)))))

(defun leisure-take-revert-info ()
  "Remove and return a buffer's revert info ."
  (let ((info (leisure-peek-revert-info)))
    (setq leisure-reverting-buffers (assq-delete-all (intern (buffer-name)) leisure-reverting-buffers))
    info))

(defun leisure-after-revert ()
  "Restore leisure info after revert."
  (let* ((info (leisure-take-revert-info)))
    (if info
        (progn
          (setq leisure-info info)
          (if (not leisure-mode) (leisure-mode 'toggle))
          (leisure-add-change-hooks)
          (leisure-change 0 -1 (leisure-buf-string))))))

(defun leisure-add-change-hooks ()
  "Add change hooks for buffer."
  (if (not (memq 'leisure-after-change after-change-functions))
      (progn
        (add-hook 'kill-buffer-hook 'leisure-disconnect nil t)
        (add-hook 'after-change-functions 'leisure-after-change nil t)
        (add-hook 'before-revert-hook 'leisure-before-revert nil t)
        (add-hook 'after-revert-hook 'leisure-after-revert nil t))))

(defun leisure-diag (level &rest args)
  (if (<= leisure-verbosity level)
      (apply 'message args)))

(defun leisure-call-program (input &rest args)
  (apply 'leisure-call-program-filter input 'leisure-no-parse args))

(defun leisure-no-parse (status buf)
  (and (eql 0 status) buf))

(defun leisure-parse (status buf)
  (with-current-buffer buf
    (let ((result (json-parse-buffer :null-object nil)))
      (if (eql status 0)
          ;; on success, return the result
          result
        ;;otherwise turn off leisure mode and return nil
        (leisure-diag 0 "Error parsing output: %2" (gethash "message" (cdr result)))
        (leisure-mode false)
        nil))))

(defun leisure-call-program-filter (input filter &rest args)
  (let ((tmp (get-buffer-create (leisure-data-com-buffer-name leisure-info)))
        (original-args args)
        (call 'call-process)
        (status 0))
    (setq args (dl/append (dl leisure-program nil `(,tmp "/tmp/leisure-errors") nil) (apply 'dl args)))
    (with-current-buffer tmp
      (erase-buffer)
      (if input
          (progn
            (setq call 'call-process-region)
            (let* ((a (dl/resolve args))
                   (prog (car a))
                   (call-args (cddddr a)))
              (message "CALLING %s %s" prog (string-join call-args " ")))
            (setq args (dl/append (dl input nil) args))))
      (setq args (dl/resolve args))
      (setq status (apply call args))
      (goto-char 0)
      (leisure-diag 1 "Got response for %s:\n%s" original-args (buffer-string))
      (if (not (eql 0 status)) (leisure-diag 0 "LEISURE ERROR: %d\n  %s" status tmp))
      (cons status (apply filter status tmp ())))))

(defun leisure-full-path ()
  (f-canonical (buffer-file-name)))

;; queue an edit. Wait at least min-wait time for activity.
;; Whenever there is more activity during the wait, wait again
;; for min-wait, up to a maximum total wait time of max-wait
(defun leisure-change (offset length text)
  (leisure-diag 1 "CHANGE %d %d %s" offset length text)
  (setf (leisure-data-changes leisure-info)
        (dl/append (leisure-data-changes leisure-info)
                   (dl (leisure-map "offset" offset "length" length "text" text))))
  ;; queue edit
  (if (not (leisure-data-flush-timer leisure-info))
      (leisure-schedule-edit 'flush-timer leisure-max-wait))
  (leisure-clear-timer 'activity-timer)
  (leisure-schedule-edit 'activity-timer leisure-min-wait)
  (message "QUEUED EDIT")
)

(defun leisure-schedule-edit (slot wait-time)
  (setf (cl-struct-slot-value 'leisure-data slot leisure-info)
        (run-with-timer wait-time nil 'leisure-send-edit)))

(defun leisure-clear-timer (slot)
  (let ((value (cl-struct-slot-value 'leisure-data slot leisure-info)))
    (if value
      (progn
        (cancel-timer value)
        (setf (cl-struct-slot-value 'leisure-data slot leisure-info) nil)))))

(defun leisure-map (&rest args)
  (let ((pos 0)
        (result (make-hash-table :test 'equal)))
    (while (< (1+ pos) (length args))
      (puthash (elt args pos) (elt args (1+ pos)) result)
      (cl-incf pos 2))
    result))

(defun leisure-update ())

(defun leisure-update ()
  (if leisure-info
      (let ((buf (current-buffer)))
        (message "CALLING %s" (string-join (list leisure-program
                                                 "session" "update"
                                                 "-cookies" (leisure-cookies))
                                           " "))
        (make-process
         :name "leisure-update"
         :buffer "leisure-update"
         :command (list leisure-program
                        "session" "update"
                        "-cookies" (leisure-cookies))
         :noquery t
         :connection-type 'pty
         :sentinel (lambda (proc status) (leisure-update-result buf proc status))
         :stderr "leisure-update-errors"))))

(defun leisure-update-result (buf proc status)
  (message "got update result: %s, buffer: %s" status (buffer-name))
  (if (string-equal status "finished\n")
      (with-current-buffer "leisure-update"
        (let ((result (downcase (string-trim (buffer-string)))))
          (erase-buffer)
          (message "MALUBA! '%s'" result)
          (if (string-equal "true" result)
              (with-current-buffer buf
                (leisure-send-edit))
            (leisure-update))))))

;; cancel pending update
(defun leisure-cancel-update ()
  (let ((status (process-status "leisure-update")))
    (cond ((memq status '(run open))
           (delete-process "leisure-update")))))

(defun leisure-send-edit ()
  (if leisure-info
      (let ((repls (dl/resolve (leisure-data-changes leisure-info)))
            (start (if (use-region-p) (region-beginning) (point)))
            (len (if (use-region-p) (- (region-end) start) 0)))
        (leisure-diag 0 "SEND EDIT!")
        (cl-incf start -1)
        (setf (leisure-data-changes leisure-info) (dl))
        (leisure-clear-timer 'activity-timer)
        (leisure-clear-timer 'flush-timer)
        (leisure-cancel-update)
        (message "selection offset: %s len: %s" start len)
        (let* ((input (leisure-map
                       "selectionOffset" start
                       "selectionLength" len
                       "replacements" repls))
               (result (leisure-call-program-filter
                        (json-encode input)
                        'leisure-parse
                        "session" "edit"
                        "-cookies" (leisure-cookies))))
          (message "RESULT %s" result)
          (if (eql 0 (car result))
              (progn
                (setq X result)
                (leisure-replacements (cdr result)))))
        (leisure-update))))

(defun leisure-cookies () (format "%s.cookies" leisure-peer))

(defun leisure-replacements (repls)
  (if repls
      (let ((selOffset (gethash "selectionOffset" repls))
            (selLength (gethash "selectionLength" repls))
            (replacements (gethash "replacements" repls)))
        (leisure-monitor-changes nil)
        (undo-boundary)
        (seq-doseq (repl replacements)
          (let ((offset (gethash "offset" repl))
                (length (gethash "length" repl))
                (text (gethash "text" repl)))
            (cl-incf offset)
            (delete-region offset (+ offset length))
            (goto-char offset)
            (insert text)))
        (undo-boundary)
        (if (> selOffset -1)
            (progn
              (cl-incf selOffset)
              (goto-char selOffset)
              (if (> selLength 0)
                  (set-mark (+ selOffset selLength))
                (deactivate-mark))))
        (leisure-monitor-changes t))))

(provide 'leisure-mode)
;;; leisure.el ends here
