import React from "react";
import styles from "./styles.css";
import PropTypes from "prop-types";
import { classNames } from "src/utils";

const Spinner = ({ invert, width, height, className }) => {
  return (
    <div
      className={classNames(
        className,
        invert ? styles.spinnerInvert : styles.spinner
      )}
      style={{ width, height }}
    />
  );
};

Spinner.propTypes = {
  width: PropTypes.string,
  height: PropTypes.string,
  invert: PropTypes.bool,
  className: PropTypes.string
};

export default Spinner;
